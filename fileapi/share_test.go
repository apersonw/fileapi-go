package fileapi

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestShares(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	f.handle("POST /open/v1/shares", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		bodies = append(bodies, body)
		ok(w, map[string]any{"code": "abcdefghijkm", "url": "https://x/s/abcdefghijkm", "password_protected": body["password"] != nil, "allow_download": true})
	})
	f.handle("GET /open/v1/shares", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("query = %v", r.URL.Query())
		}
		ok(w, map[string]any{"list": []map[string]any{{"code": "abcdefghijkm"}}, "total": 21, "page": 2, "page_size": 20})
	})
	f.handle("GET /open/v1/shares/abcdefghijkm", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"code": "abcdefghijkm", "downloads": 3, "views": 5})
	})
	f.handle("DELETE /open/v1/shares/abcdefghijkm", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	c := f.client(t)
	ctx := context.Background()
	s, err := c.ShareFiles(ctx, []int64{1, 2}, &ShareOptions{Password: "Ab12", ExpiresIn: time.Hour, MaxDownloads: 3, PreviewOnly: true, Title: "资料"})
	if err != nil || !s.PasswordProtected || s.URL != "https://x/s/abcdefghijkm" {
		t.Fatalf("share = %+v, %v", s, err)
	}
	if _, err := c.ShareFolder(ctx, "/合同/", true, nil); err != nil {
		t.Fatal(err)
	}
	b := bodies[0]
	if b["password"] != "Ab12" || b["expires_in"] != float64(3600) || b["max_downloads"] != float64(3) || b["allow_download"] != false ||
		b["title"] != "资料" || len(b["file_ids"].([]any)) != 2 {
		t.Fatalf("body = %v", b)
	}
	if bodies[1]["folder"] != "/合同/" || bodies[1]["recursive"] != true {
		t.Fatalf("folder body = %v", bodies[1])
	}
	if p, err := c.Shares(ctx, 2, 0); err != nil || p.Total != 21 || len(p.List) != 1 {
		t.Fatalf("list = %+v, %v", p, err)
	}
	if s, err := c.Share(ctx, "abcdefghijkm"); err != nil || s.Downloads != 3 || s.Views != 5 {
		t.Fatalf("get = %+v, %v", s, err)
	}
	if err := c.DeleteShare(ctx, "abcdefghijkm"); err != nil {
		t.Fatal(err)
	}
}
