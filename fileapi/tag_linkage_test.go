package fileapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTagLinkage(t *testing.T) {
	f := newFakeServer(t)
	f.storage()
	content := []byte("tagged")
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("tag_ids") != "3,5" {
			t.Errorf("proxy tag_ids = %q", r.FormValue("tag_ids"))
		}
		ok(w, map[string]any{"id": 7, "tags": []map[string]any{{"id": 3, "name": "合同"}, {"id": 5, "name": "归档"}}})
	})
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if ids, _ := req["tag_ids"].([]any); len(ids) != 1 || ids[0] != float64(3) {
			t.Errorf("direct tag_ids = %v", req["tag_ids"])
		}
		ok(w, map[string]any{"file": map[string]any{"id": 9}, "upload": f.presigned("/storage/obj", int64(len(content)), "")})
	})
	f.handle("POST /open/v1/files/9/complete", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 9}) })
	f.handle("POST /open/v1/shares", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if ids, _ := req["tag_ids"].([]any); len(ids) != 2 || req["file_ids"] != nil {
			t.Errorf("share = %v", req)
		}
		ok(w, map[string]any{"code": "abc", "file_ids": []int64{7, 9}})
	})
	c := f.client(t)
	ctx := context.Background()
	res, err := c.Upload(ctx, "a.txt", bytes.NewReader(content), int64(len(content)), &UploadOptions{TagIDs: []int64{3, 5}})
	if err != nil || len(res.Tags) != 2 {
		t.Fatalf("upload = %+v, %v", res, err)
	}
	if _, err := c.UploadDirect(ctx, "a.txt", bytes.NewReader(content), int64(len(content)), &DirectUploadOptions{TagIDs: []int64{3}}); err != nil {
		t.Fatal(err)
	}
	if sh, err := c.ShareTags(ctx, []int64{3, 5}, nil); err != nil || len(sh.FileIDs) != 2 {
		t.Fatalf("share = %+v, %v", sh, err)
	}
	// 本地签发的上传令牌带标签；不能与 VersionOf 同时使用
	tok, err := c.NewUploadToken(&UploadTokenOptions{TagIDs: []int64{3}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok.Token, ".")[0])
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	if ids, _ := claims["tag_ids"].([]any); len(ids) != 1 || ids[0] != float64(3) {
		t.Fatalf("claims = %v", claims)
	}
	if _, err := c.NewUploadToken(&UploadTokenOptions{TagIDs: []int64{3}, VersionOf: 7}); err == nil {
		t.Fatal("TagIDs with VersionOf should be rejected")
	}
}
