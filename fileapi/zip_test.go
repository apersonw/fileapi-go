package fileapi

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestZip(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/7/zip", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"total": 2, "truncated": false, "total_size": 5, "entries": []map[string]any{
			{"index": 0, "path": "docs/", "dir": true, "size": 0, "downloadable": false},
			{"index": 1, "path": "docs/说明.txt", "size": 5, "compressed_size": 5, "downloadable": true, "modified_at": "2026-10-01T08:00:00Z"},
		}})
	})
	f.handle("GET /open/v1/files/7/zip/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="a.txt"`)
		_, _ = w.Write([]byte("hello"))
	})
	f.handle("GET /open/v1/files/7/zip/0", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusBadRequest, CodeBadRequest, "该条目是目录，不能下载")
	})
	c := f.client(t)
	ctx := context.Background()
	l, err := c.ZipEntries(ctx, 7)
	if err != nil || l.Total != 2 || !l.Entries[0].Dir || l.Entries[1].Path != "docs/说明.txt" || !l.Entries[1].Downloadable || l.Entries[1].ModifiedAt == nil {
		t.Fatalf("entries = %+v, %v", l, err)
	}
	obj, err := c.OpenZipEntry(ctx, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(obj)
	obj.Close()
	if string(data) != "hello" {
		t.Fatalf("entry = %q", data)
	}
	if _, err := c.OpenZipEntry(ctx, 7, 0); !IsCode(err, CodeBadRequest) {
		t.Fatalf("dir entry = %v", err)
	}
}
