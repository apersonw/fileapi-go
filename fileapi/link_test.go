package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestLimitedLinks(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/7/url", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("max_visits") != "3" || q.Get("revocable") != "true" || q.Get("expires") != "60" || q.Get("password") != "ab12" {
			t.Errorf("query = %v", q)
		}
		ok(w, map[string]any{"url": "http://files.test/a/x", "mode": "gateway", "expires_at": "2026-10-12T10:00:00Z", "link_id": "abc", "max_visits": 3, "password_protected": true})
	})
	f.handle("GET /open/v1/links/abc", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"link_id": "abc", "file_id": 7, "max_visits": 3, "visits": 1, "password_protected": true, "expires_at": "2026-10-12T10:00:00Z"})
	})
	revoked := false
	f.handle("DELETE /open/v1/links/abc", func(w http.ResponseWriter, r *http.Request) {
		revoked = true
		ok(w, nil)
	})
	c := f.client(t)
	ctx := context.Background()
	u, err := c.AccessURL(ctx, 7, &URLOptions{Expires: 60e9, MaxVisits: 3, Revocable: true, Password: "ab12"})
	if err != nil || u.LinkID != "abc" || u.MaxVisits != 3 || !u.PasswordProtected {
		t.Fatalf("url = %+v, %v", u, err)
	}
	l, err := c.LinkStatus(ctx, u.LinkID)
	if err != nil || l.Visits != 1 || l.MaxVisits != 3 || l.FileID != 7 || !l.PasswordProtected {
		t.Fatalf("status = %+v, %v", l, err)
	}
	if err := c.RevokeLink(ctx, u.LinkID); err != nil || !revoked {
		t.Fatalf("revoke: %v", err)
	}
}
