package fileapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRetention(t *testing.T) {
	f := newFakeServer(t)
	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	f.handle("PUT /open/v1/files/7/retention", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if _, has := req["legal_hold"]; has {
			if req["legal_hold"] != true || len(req) != 1 {
				t.Errorf("legal hold body = %v", req)
			}
			ok(w, map[string]any{"id": 7, "legal_hold": true, "locked": true})
			return
		}
		if req["retain_until"] != "2027-01-02T03:04:05Z" {
			t.Errorf("retain_until = %v", req["retain_until"])
		}
		ok(w, map[string]any{"id": 7, "retain_until": "2027-01-02T03:04:05Z", "locked": true})
	})
	f.handle("DELETE /open/v1/files/7", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusLocked, CodeFileLocked, "文件保留至 2027-01-02T03:04:05Z，期内不能删除或修改内容")
	})
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("locked") != "true" {
			t.Errorf("locked filter = %q", r.URL.RawQuery)
		}
		ok(w, map[string]any{"list": []map[string]any{{"id": 7, "locked": true}}, "page_size": 20})
	})

	c := f.client(t)
	ctx := context.Background()
	file, err := c.SetRetention(ctx, 7, RetentionOptions{RetainUntil: &until})
	if err != nil || !file.Locked || file.RetainUntil == nil || !file.RetainUntil.Equal(until) {
		t.Fatalf("set retention = %+v, %v", file, err)
	}
	hold := true
	if file, err = c.SetRetention(ctx, 7, RetentionOptions{LegalHold: &hold}); err != nil || !file.LegalHold {
		t.Fatalf("legal hold = %+v, %v", file, err)
	}
	var apiErr *Error
	if err := c.Delete(ctx, 7); !errors.As(err, &apiErr) || apiErr.Code != CodeFileLocked {
		t.Fatalf("delete locked = %v", err)
	}
	locked := true
	page, err := c.List(ctx, &ListQuery{Locked: &locked})
	if err != nil || len(page.List) != 1 || !page.List[0].Locked {
		t.Fatalf("list locked = %+v, %v", page, err)
	}
}
