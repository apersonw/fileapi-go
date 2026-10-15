package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestTagRules(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	rule := map[string]any{"id": 3, "name": "合同 PDF", "tag_ids": []int64{1}, "extensions": []string{".pdf"}, "folder": "/合同/", "enabled": true}
	save := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		bodies = append(bodies, body)
		ok(w, rule)
	}
	f.handle("GET /open/v1/tag-rules", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"list": []any{rule}}) })
	f.handle("POST /open/v1/tag-rules", save)
	f.handle("PUT /open/v1/tag-rules/3", save)
	f.handle("POST /open/v1/tag-rules/preview", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"files": 5, "over_limit": 1}) })
	f.handle("POST /open/v1/tag-rules/3/apply", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"updated": 5, "remaining": false}) })
	f.handle("DELETE /open/v1/tag-rules/3", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	c := f.client(t)
	ctx := context.Background()
	in := TagRule{Name: "合同 PDF", TagIDs: []int64{1}, Extensions: []string{"pdf"}, Folder: "/合同", Recursive: true, Enabled: true, ID: 99}
	got, err := c.CreateTagRule(ctx, in)
	if err != nil || got.ID != 3 || got.Folder != "/合同/" {
		t.Fatalf("create = %+v, %v", got, err)
	}
	b := bodies[0]
	if b["name"] != "合同 PDF" || b["folder"] != "/合同" || b["recursive"] != true || b["enabled"] != true || b["id"] != nil || b["category"] != nil {
		t.Fatalf("body = %v", b)
	}
	in.Enabled = false
	if _, err := c.UpdateTagRule(ctx, 3, in); err != nil || bodies[1]["enabled"] != false {
		t.Fatalf("update body = %v, %v", bodies[1], err)
	}
	if list, err := c.ListTagRules(ctx); err != nil || len(list) != 1 || list[0].Extensions[0] != ".pdf" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if p, err := c.PreviewTagRule(ctx, in); err != nil || p.Files != 5 || p.OverLimit != 1 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if a, err := c.ApplyTagRule(ctx, 3); err != nil || a.Updated != 5 || a.Remaining {
		t.Fatalf("apply = %+v, %v", a, err)
	}
	if err := c.DeleteTagRule(ctx, 3); err != nil {
		t.Fatal(err)
	}
}
