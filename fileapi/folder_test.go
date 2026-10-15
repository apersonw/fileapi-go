package fileapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestFolders(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/folders", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parent") != "/合同" {
			t.Errorf("parent = %q", r.URL.Query().Get("parent"))
		}
		ok(w, map[string]any{"path": "/合同/", "files": 1, "bytes": 10, "folders": []map[string]any{{"path": "/合同/2026/", "name": "2026", "files": 3, "bytes": 30}}})
	})
	f.handle("POST /open/v1/files/move", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs    []int64 `json:"ids"`
			Folder string  `json:"folder"`
		}
		readJSON(t, r, &body)
		if len(body.IDs) != 2 || body.Folder != "/归档" {
			t.Errorf("move body = %+v", body)
		}
		ok(w, map[string]any{"moved": 2})
	})
	f.handle("PATCH /open/v1/files/7", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		readJSON(t, r, &body)
		ok(w, map[string]any{"id": 7, "folder": body["folder"] + "/"})
	})
	f.handle("POST /open/v1/folders/rename", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		readJSON(t, r, &body)
		if body["from"] != "/a" || body["to"] != "/b" {
			t.Errorf("rename body = %v", body)
		}
		ok(w, map[string]any{"moved": 4})
	})
	f.handle("DELETE /open/v1/folders", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") != "/tmp" || r.URL.Query().Get("permanent") != "true" {
			t.Errorf("delete query = %v", r.URL.Query())
		}
		ok(w, map[string]any{"deleted": 3, "trashed": 0, "failed": 0, "remaining": 5})
	})
	c := f.client(t)
	ctx := context.Background()
	list, err := c.ListFolders(ctx, "/合同")
	if err != nil || list.Path != "/合同/" || list.Files != 1 || len(list.Folders) != 1 || list.Folders[0].Name != "2026" || list.Folders[0].Files != 3 {
		t.Fatalf("folders = %+v, %v", list, err)
	}
	if n, err := c.MoveFiles(ctx, []int64{1, 2}, "/归档"); err != nil || n != 2 {
		t.Fatalf("move = %d, %v", n, err)
	}
	if _, err := c.MoveFiles(ctx, nil, "/"); err == nil {
		t.Fatal("empty ids should fail")
	}
	if file, err := c.MoveFile(ctx, 7, "/x"); err != nil || file.Folder != "/x/" {
		t.Fatalf("move file = %+v, %v", file, err)
	}
	if n, err := c.RenameFolder(ctx, "/a", "/b"); err != nil || n != 4 {
		t.Fatalf("rename = %d, %v", n, err)
	}
	if res, err := c.DeleteFolder(ctx, "/tmp", true); err != nil || res.Deleted != 3 || res.Remaining != 5 {
		t.Fatalf("delete = %+v, %v", res, err)
	}
}

func TestFolderOptions(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("folder") != "/合同" || r.URL.Query().Get("recursive") != "true" {
			t.Errorf("list query = %v", r.URL.Query())
		}
		ok(w, map[string]any{"list": []map[string]any{{"id": 1, "folder": "/合同/2026/"}}, "page_size": 20})
	})
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("folder") != "/上传" {
			t.Errorf("upload folder = %q, %v", r.FormValue("folder"), err)
		}
		ok(w, map[string]any{"id": 2, "folder": "/上传/"})
	})
	f.handle("POST /open/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		if body["folder"] != "/合同" || body["files"] != nil {
			t.Errorf("archive body = %v", body)
		}
		ok(w, map[string]any{"url": "http://x/z/1", "files": 3})
	})
	c := f.client(t)
	ctx := context.Background()
	page, err := c.List(ctx, &ListQuery{Folder: "/合同", Recursive: true})
	if err != nil || page.List[0].Folder != "/合同/2026/" {
		t.Fatalf("list = %+v, %v", page, err)
	}
	data := []byte("hello")
	res, err := c.Upload(ctx, "a.txt", bytes.NewReader(data), int64(len(data)), &UploadOptions{Folder: "/上传"})
	if err != nil || res.Folder != "/上传/" {
		t.Fatalf("upload = %+v, %v", res, err)
	}
	if a, err := c.CreateArchive(ctx, nil, &ArchiveOptions{Folder: "/合同"}); err != nil || a.Files != 3 {
		t.Fatalf("archive = %+v, %v", a, err)
	}
	if _, err := c.NewUploadToken(&UploadTokenOptions{VersionOf: 1, Folder: "/x"}); err == nil || !strings.Contains(err.Error(), "Folder") {
		t.Fatalf("version + folder token: %v", err)
	}
	tok, err := c.NewUploadToken(&UploadTokenOptions{Folder: "/头像"})
	if err != nil || !strings.Contains(tokenPayload(tok.Token), `"folder":"/头像"`) {
		t.Fatalf("token = %v", err)
	}
}

func tokenPayload(token string) string {
	raw, _ := base64.RawURLEncoding.DecodeString(strings.SplitN(token, ".", 2)[0])
	return string(raw)
}
