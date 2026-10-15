package fileapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestFileVersions(t *testing.T) {
	f := newFakeServer(t)
	f.storage()
	content := []byte("new version")
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("version_of") != "7" || r.FormValue("object_type") != "" {
			t.Errorf("proxy version_of = %q", r.FormValue("version_of"))
		}
		ok(w, map[string]any{"id": 7, "version": 2})
	})
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["version_of"] != float64(7) {
			t.Errorf("direct version_of = %v", req["version_of"])
		}
		ok(w, map[string]any{"file": map[string]any{"id": 9}, "upload": f.presigned("/storage/obj", int64(len(content)), "")})
	})
	f.handle("POST /open/v1/files/9/complete", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 7, "version": 3}) })
	f.handle("GET /open/v1/files/7/versions", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"list": []map[string]any{{"id": 11, "version": 2, "original_filename": "a.txt", "size": 3, "created_at": "2026-10-12T10:00:00Z"}}})
	})
	f.handle("GET /open/v1/files/7/versions/11/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="a.txt"`)
		_, _ = w.Write([]byte("old"))
	})
	f.handle("POST /open/v1/files/7/versions/11/restore", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 7, "version": 4}) })
	f.handle("DELETE /open/v1/files/7/versions/11", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })

	c := f.client(t)
	ctx := context.Background()
	// 上传新版本时不尝试秒传
	res, err := c.Upload(ctx, "a.txt", bytes.NewReader(content), int64(len(content)), &UploadOptions{VersionOf: 7, TryInstant: true})
	if err != nil || res.ID != 7 || res.Version != 2 || f.count("POST /open/v1/files/instant") != 0 {
		t.Fatalf("upload version = %+v, %v", res, err)
	}
	file, err := c.UploadDirect(ctx, "a.txt", bytes.NewReader(content), int64(len(content)), &DirectUploadOptions{VersionOf: 7})
	if err != nil || file.ID != 7 || file.Version != 3 {
		t.Fatalf("direct version = %+v, %v", file, err)
	}
	list, err := c.ListVersions(ctx, 7)
	if err != nil || len(list) != 1 || list[0].ID != 11 || list[0].Version != 2 || list[0].OriginalFilename != "a.txt" {
		t.Fatalf("versions = %+v, %v", list, err)
	}
	obj, err := c.OpenVersion(ctx, 7, 11)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(obj)
	obj.Close()
	if string(data) != "old" || obj.Filename != "a.txt" {
		t.Fatalf("version content = %q %q", data, obj.Filename)
	}
	if restored, err := c.RestoreVersion(ctx, 7, 11); err != nil || restored.Version != 4 {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if err := c.DeleteVersion(ctx, 7, 11); err != nil {
		t.Fatal(err)
	}
}
