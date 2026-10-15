package fileapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestCollections(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	coll := map[string]any{"code": "abcdefghijkm", "url": "https://x/c/abcdefghijkm", "folder": "/作业/", "enabled": true, "files": 2}
	f.handle("POST /open/v1/collections", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		bodies = append(bodies, body)
		ok(w, coll)
	})
	f.handle("GET /open/v1/collections", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"list": []any{coll}, "total": 1, "page": 1, "page_size": 20})
	})
	f.handle("GET /open/v1/collections/abcdefghijkm", func(w http.ResponseWriter, r *http.Request) { ok(w, coll) })
	f.handle("PATCH /open/v1/collections/abcdefghijkm", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		bodies = append(bodies, body)
		ok(w, map[string]any{"code": "abcdefghijkm", "enabled": false})
	})
	f.handle("DELETE /open/v1/collections/abcdefghijkm", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		var meta map[string]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("metadata")), &meta)
		if meta["collection"] != "abcdefghijkm" || meta["uploader"] != "张三" {
			t.Errorf("metadata = %v", meta)
		}
		ok(w, map[string]any{"list": []any{map[string]any{"id": 7}}, "page_size": 20})
	})
	c := f.client(t)
	ctx := context.Background()
	if _, err := c.CreateCollection(ctx, "", nil); err == nil {
		t.Fatal("empty title should fail")
	}
	got, err := c.CreateCollection(ctx, "作业", &CollectionOptions{Folder: "/作业", TagIDs: []int64{3}, Extensions: []string{"pdf"}, MaxFiles: 10,
		GroupByUploader: true, Password: "Ab12", ExpiresIn: time.Hour})
	if err != nil || got.URL != "https://x/c/abcdefghijkm" || got.Files != 2 {
		t.Fatalf("create = %+v, %v", got, err)
	}
	b := bodies[0]
	if b["title"] != "作业" || b["folder"] != "/作业" || b["max_files"] != float64(10) || b["group_by_uploader"] != true ||
		b["password"] != "Ab12" || b["expires_in"] != float64(3600) || len(b["tag_ids"].([]any)) != 1 || b["require_name"] != nil {
		t.Fatalf("body = %v", b)
	}
	if p, err := c.Collections(ctx, 0, 0); err != nil || p.Total != 1 {
		t.Fatalf("list = %+v, %v", p, err)
	}
	if g, err := c.Collection(ctx, "abcdefghijkm"); err != nil || g.Folder != "/作业/" {
		t.Fatalf("get = %+v, %v", g, err)
	}
	off, zero := false, time.Duration(0)
	if u, err := c.UpdateCollection(ctx, "abcdefghijkm", CollectionUpdate{Enabled: &off, ExpiresIn: &zero}); err != nil || u.Enabled {
		t.Fatalf("update = %+v, %v", u, err)
	}
	if bodies[1]["enabled"] != false || bodies[1]["expires_in"] != float64(0) || len(bodies[1]) != 2 {
		t.Fatalf("update body = %v", bodies[1])
	}
	if p, err := c.CollectionFiles(ctx, "abcdefghijkm", &ListQuery{Metadata: map[string]string{"uploader": "张三", "collection": "x"}}); err != nil || len(p.List) != 1 {
		t.Fatalf("files = %+v, %v", p, err)
	}
	if err := c.DeleteCollection(ctx, "abcdefghijkm"); err != nil {
		t.Fatal(err)
	}
}
