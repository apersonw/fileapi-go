package fileapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestExportFiles(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/export", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("folder") != "/a" || q.Get("tag_id") != "3" || q.Has("page_size") || q.Has("cursor") {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Trailer", "X-Export-Rows, X-Export-Truncated")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write([]byte("\xef\xbb\xbfid\n1\n2\n"))
		w.Header().Set("X-Export-Rows", "2")
		w.Header().Set("X-Export-Truncated", "false")
	})
	c := f.client(t)
	var buf bytes.Buffer
	res, err := c.ExportFiles(context.Background(), &ListQuery{Folder: "/a", TagIDs: []int64{3}, PageSize: 50, Cursor: "x"}, &buf)
	if err != nil || res.Rows != 2 || res.Truncated || buf.String() != "\xef\xbb\xbfid\n1\n2\n" {
		t.Fatalf("export = %+v, %q, %v", res, buf.String(), err)
	}
}
