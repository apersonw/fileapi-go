package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestMetadataSearchAndUpdate(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("metadata") != `{"project":"alpha"}` || len(q["metadata_key"]) != 2 || q["metadata_key"][1] != "stage" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		ok(w, map[string]any{"list": []map[string]any{{"id": 1}}, "page_size": 20})
	})
	f.handle("PATCH /open/v1/files/1", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Metadata map[string]*string `json:"metadata"`
		}
		readJSON(t, r, &req)
		if len(req.Metadata) != 2 || *req.Metadata["stage"] != "final" || req.Metadata["draft"] != nil {
			t.Errorf("patch = %+v", req.Metadata)
		}
		ok(w, map[string]any{"id": 1, "metadata": map[string]string{"stage": "final"}})
	})
	c := f.client(t)
	page, err := c.List(context.Background(), &ListQuery{Metadata: map[string]string{"project": "alpha"}, MetadataKeys: []string{"owner", "stage"}})
	if err != nil || len(page.List) != 1 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	file, err := c.UpdateMetadata(context.Background(), 1, map[string]string{"stage": "final"}, "draft")
	if err != nil || file.Metadata["stage"] != "final" {
		t.Fatalf("update = %+v, %v", file, err)
	}
}
