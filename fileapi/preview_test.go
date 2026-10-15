package fileapi

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreview(t *testing.T) {
	f := newFakeServer(t)
	var polls atomic.Int32
	f.handle("GET /open/v1/files/5/preview", func(w http.ResponseWriter, r *http.Request) {
		status := PreviewRunning
		if polls.Add(1) >= 2 {
			status = PreviewReady
		}
		ok(w, map[string]any{"status": status, "available": status == PreviewReady, "pdf_bytes": 1024})
	})
	f.handle("POST /open/v1/files/5/preview", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"status": PreviewPending})
	})
	f.handle("GET /open/v1/files/5/url", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("preview") != "pdf" {
			t.Errorf("query = %v", r.URL.Query())
		}
		ok(w, map[string]any{"url": "https://x/a/tok", "mode": "gateway"})
	})
	c := f.client(t)
	ctx := context.Background()
	if p, err := c.RequestPreview(ctx, 5); err != nil || p.Status != PreviewPending {
		t.Fatalf("request = %+v, %v", p, err)
	}
	p, err := c.WaitPreview(ctx, 5, 10*time.Millisecond)
	if err != nil || !p.Available || p.PDFBytes != 1024 || polls.Load() != 2 {
		t.Fatalf("wait = %+v, %v", p, err)
	}
	if u, err := c.AccessURL(ctx, 5, &URLOptions{Preview: true}); err != nil || u.URL != "https://x/a/tok" {
		t.Fatalf("url = %+v, %v", u, err)
	}
}
