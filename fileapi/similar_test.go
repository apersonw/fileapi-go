package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestSimilarImages(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/5/similar", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("threshold") != "0" || q.Get("limit") != "5" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		ok(w, map[string]any{"hashed": true, "list": []any{map[string]any{"file": map[string]any{"id": 6}, "distance": 2}}})
	})
	f.handle("GET /open/v1/files/similar-groups", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("folder") != "/相册/" || q.Has("threshold") {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		ok(w, map[string]any{"groups": []any{map[string]any{"files": []any{
			map[string]any{"file": map[string]any{"id": 8}, "distance": 0}, map[string]any{"file": map[string]any{"id": 7}, "distance": 3},
		}}}, "total": 1, "scanned": 10})
	})
	c := f.client(t)
	ctx := context.Background()
	zero := 0
	res, err := c.SimilarImages(ctx, 5, &SimilarOptions{Threshold: &zero, Limit: 5})
	if err != nil || !res.Hashed || len(res.List) != 1 || res.List[0].File.ID != 6 || res.List[0].Distance != 2 {
		t.Fatalf("similar = %+v, %v", res, err)
	}
	groups, err := c.SimilarImageGroups(ctx, &SimilarOptions{Folder: "/相册/"})
	if err != nil || groups.Total != 1 || groups.Scanned != 10 || len(groups.Groups[0].Files) != 2 || groups.Groups[0].Files[1].File.ID != 7 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
}
