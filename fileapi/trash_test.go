package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestTrash(t *testing.T) {
	f := newFakeServer(t)
	f.handle("DELETE /open/v1/files/2", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"trashed": r.URL.Query().Get("permanent") != "true"})
	})
	f.handle("POST /open/v1/files/2/restore", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"id": 2, "status": FileStatusActive})
	})
	f.handle("GET /open/v1/trash", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("object_type") != "avatar" || r.URL.Query().Get("min_width") != "100" || r.URL.Query().Get("max_duration_ms") != "5000" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		ok(w, map[string]any{"list": []map[string]any{{"id": 2, "status": FileStatusTrashed, "trashed_at": "2026-10-12T10:00:00Z", "purge_at": "2026-10-19T10:00:00Z", "width": 640, "height": 480, "duration_ms": 1200}}, "page_size": 20})
	})
	f.handle("DELETE /open/v1/trash", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"scheduled": 5}) })
	var permanent []bool
	f.handle("POST /open/v1/files/batch-delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IDs       []int64 `json:"ids"`
			Permanent bool    `json:"permanent"`
		}
		readJSON(t, r, &req)
		permanent = append(permanent, req.Permanent)
		trashed := []int64{}
		if !req.Permanent {
			trashed = req.IDs
		}
		ok(w, map[string]any{"deleted": req.IDs, "trashed": trashed, "failed": map[string]string{}})
	})
	c := f.client(t)
	ctx := context.Background()
	if err := c.Delete(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePermanently(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if file, err := c.Restore(ctx, 2); err != nil || file.Status != FileStatusActive {
		t.Fatalf("restore = %+v, %v", file, err)
	}
	page, err := c.ListTrash(ctx, &ListQuery{ObjectType: "avatar", MinWidth: 100, MaxDurationMS: 5000})
	if err != nil || len(page.List) != 1 || page.List[0].Status != FileStatusTrashed || page.List[0].PurgeAt == nil || page.List[0].TrashedAt == nil ||
		*page.List[0].Width != 640 || *page.List[0].DurationMS != 1200 {
		t.Fatalf("list trash = %+v, %v", page, err)
	}
	if n, err := c.EmptyTrash(ctx); err != nil || n != 5 {
		t.Fatalf("empty trash = %d, %v", n, err)
	}
	if res, err := c.BatchDelete(ctx, []int64{1, 2}); err != nil || len(res.Trashed) != 2 {
		t.Fatalf("batch delete = %+v, %v", res, err)
	}
	if res, err := c.BatchDeletePermanently(ctx, []int64{1, 2}); err != nil || len(res.Trashed) != 0 || len(res.Deleted) != 2 {
		t.Fatalf("batch delete permanently = %+v, %v", res, err)
	}
	if len(permanent) != 2 || permanent[0] || !permanent[1] {
		t.Fatalf("permanent flags = %v", permanent)
	}
	if f.count("DELETE /open/v1/files/2") != 2 {
		t.Fatalf("delete calls = %d", f.count("DELETE /open/v1/files/2"))
	}
}
