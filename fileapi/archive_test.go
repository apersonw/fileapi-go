package fileapi

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestCreateArchive(t *testing.T) {
	f := newFakeServer(t)
	f.handle("POST /open/v1/archives", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Files     []ArchiveFile `json:"files"`
			Filename  string        `json:"filename"`
			ExpiresIn int64         `json:"expires_in"`
			Compress  bool          `json:"compress"`
		}
		readJSON(t, r, &req)
		if len(req.Files) != 2 || req.Files[0].Name != "docs/a.txt" || req.Files[1].Name != "" || req.Filename != "pack" || req.ExpiresIn != 600 || !req.Compress {
			t.Errorf("request = %+v", req)
		}
		ok(w, map[string]any{"url": "http://files.test/z/abc", "filename": "pack.zip", "files": 2, "bytes": 10, "expires_at": "2026-10-12T10:00:00Z"})
	})
	a, err := f.client(t).CreateArchive(context.Background(), []ArchiveFile{{ID: 1, Name: "docs/a.txt"}, {ID: 2}},
		&ArchiveOptions{Filename: "pack", ExpiresIn: 10 * time.Minute, Compress: true})
	if err != nil || a.URL != "http://files.test/z/abc" || a.Files != 2 || a.Filename != "pack.zip" {
		t.Fatalf("archive = %+v, %v", a, err)
	}
}
