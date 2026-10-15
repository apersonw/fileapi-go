package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestHotFiles(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/hot-files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("days") != "30" || r.URL.Query().Get("limit") != "5" {
			t.Errorf("query = %v", r.URL.Query())
		}
		ok(w, map[string]any{"list": []map[string]any{{"file": map[string]any{"id": 7, "access_count": 12, "last_accessed_at": "2026-10-12T10:00:00Z"}, "visits": 9, "bytes": 900}}})
	})
	list, err := f.client(t).HotFiles(context.Background(), 30, 5)
	if err != nil || len(list) != 1 || list[0].File.ID != 7 || list[0].Visits != 9 || list[0].File.AccessCount != 12 || list[0].File.LastAccessedAt == nil {
		t.Fatalf("hot = %+v, %v", list, err)
	}
}
