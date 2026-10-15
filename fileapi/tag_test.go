package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestTags(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/tags", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"list": []map[string]any{{"id": 1, "name": "合同", "color": "blue", "files": 3}}})
	})
	f.handle("POST /open/v1/tags", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["name"] != "合同" || req["color"] != "blue" {
			t.Errorf("create = %v", req)
		}
		ok(w, map[string]any{"id": 1, "name": "合同", "color": "blue"})
	})
	f.handle("PATCH /open/v1/tags/1", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 1, "name": "合同-2026"}) })
	f.handle("DELETE /open/v1/tags/1", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"untagged_files": 3}) })
	f.handle("PUT /open/v1/files/7/tags", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if ids, _ := req["tag_ids"].([]any); len(ids) != 0 {
			t.Errorf("clear tags = %v", req)
		}
		ok(w, map[string]any{"id": 7})
	})
	f.handle("POST /open/v1/files/batch-tag", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if len(req["ids"].([]any)) != 2 || len(req["add"].([]any)) != 1 {
			t.Errorf("batch = %v", req)
		}
		ok(w, map[string]any{"updated": 2})
	})
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q["tag_id"]) != 2 || q.Get("tag_mode") != "any" {
			t.Errorf("list query = %q", r.URL.RawQuery)
		}
		ok(w, map[string]any{"list": []map[string]any{{"id": 7, "tags": []map[string]any{{"id": 1, "name": "合同"}}}}, "page_size": 20})
	})
	c := f.client(t)
	ctx := context.Background()
	if tags, err := c.ListTags(ctx); err != nil || len(tags) != 1 || tags[0].Files != 3 {
		t.Fatalf("list = %+v, %v", tags, err)
	}
	if tg, err := c.CreateTag(ctx, "合同", TagBlue); err != nil || tg.ID != 1 {
		t.Fatalf("create = %+v, %v", tg, err)
	}
	if tg, err := c.UpdateTag(ctx, 1, "合同-2026", ""); err != nil || tg.Name != "合同-2026" {
		t.Fatalf("update = %+v, %v", tg, err)
	}
	if n, err := c.DeleteTag(ctx, 1); err != nil || n != 3 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	if _, err := c.SetFileTags(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := c.BatchTag(ctx, []int64{7, 8}, []int64{1}, nil); err != nil || n != 2 {
		t.Fatalf("batch = %d, %v", n, err)
	}
	page, err := c.List(ctx, &ListQuery{TagIDs: []int64{1, 2}, TagAny: true})
	if err != nil || page.List[0].Tags[0].Name != "合同" {
		t.Fatalf("list files = %+v, %v", page, err)
	}
}
