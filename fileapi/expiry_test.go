package fileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestExpiryOptions(t *testing.T) {
	f := newFakeServer(t)
	store := f.storage()
	content := []byte("expiring content")
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("expires_in"); got != "91" { // 90.5 秒向上取整
			t.Errorf("proxy expires_in = %q", got)
		}
		ok(w, map[string]any{"id": 1, "expires_at": "2026-10-03T00:00:00Z"})
	})
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["expires_in"] != float64(3600) {
			t.Errorf("direct expires_in = %v", req["expires_in"])
		}
		ok(w, map[string]any{"file": map[string]any{"id": 2}, "upload": f.presigned("/storage/obj", int64(len(content)), "")})
	})
	f.handle("POST /open/v1/files/2/complete", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 2}) })
	f.handle("POST /open/v1/files/instant", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["expires_in"] != float64(60) {
			t.Errorf("instant expires_in = %v", req["expires_in"])
		}
		ok(w, map[string]any{"hit": false})
	})
	c := f.client(t)
	ctx := context.Background()
	res, err := c.Upload(ctx, "a.txt", bytes.NewReader(content), int64(len(content)), &UploadOptions{ExpiresIn: 90*time.Second + 500*time.Millisecond})
	if err != nil || res.ExpiresAt == nil || !res.ExpiresAt.Equal(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("upload = %+v, %v", res, err)
	}
	if _, err := c.UploadDirect(ctx, "b.txt", bytes.NewReader(content), int64(len(content)), &DirectUploadOptions{ExpiresIn: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.objects["/storage/obj"], content) {
		t.Fatal("direct upload content differs")
	}
	if _, err := c.UploadInstant(ctx, "c.txt", sum(content), int64(len(content)), &InstantOptions{ExpiresIn: time.Minute}); err != nil {
		t.Fatal(err)
	}
}

func TestSetExpiration(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	f.handle("PATCH /open/v1/files/7", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		bodies = append(bodies, req)
		ok(w, map[string]any{"id": 7})
	})
	c := f.client(t)
	at := time.Date(2026, 12, 31, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if _, err := c.SetExpiration(context.Background(), 7, at); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetExpiration(context.Background(), 7, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0]["expires_at"] != "2026-12-31T08:00:00+08:00" || bodies[1]["expires_at"] != "" {
		t.Fatalf("bodies = %v", bodies)
	}
}

func TestBatchExpiry(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	f.handle("POST /open/v1/files/batch-expiry", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		bodies = append(bodies, req)
		ok(w, map[string]any{"updated": 2})
	})
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expiring_days") != "7" || r.URL.Query().Get("retention_expiring_days") != "3" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		ok(w, map[string]any{"list": []any{}})
	})
	c := f.client(t)
	ctx := context.Background()
	if n, err := c.BatchRenew(ctx, []int64{1, 2}, 7*24*time.Hour); err != nil || n != 2 {
		t.Fatalf("renew = %d, %v", n, err)
	}
	if _, err := c.BatchSetExpiration(ctx, []int64{1}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BatchRenew(ctx, []int64{1}, 0); err == nil {
		t.Fatal("zero duration should fail")
	}
	if len(bodies) != 2 || bodies[0]["expires_in"] != float64(604800) || len(bodies[0]["ids"].([]any)) != 2 || bodies[1]["expires_at"] != "" {
		t.Fatalf("bodies = %v", bodies)
	}
	if _, err := c.List(ctx, &ListQuery{ExpiringDays: 7, RetentionExpiringDays: 3}); err != nil {
		t.Fatal(err)
	}
}

func TestParseExpiringEvents(t *testing.T) {
	e := WebhookEvent{Type: EventFileExpiring, Data: json.RawMessage(`{"client_id":"fa_x","kind":"expiry","days_before":3,"files":[{"id":4,"original_filename":"a.pdf","folder":"/","size":9,"expires_at":"2026-10-20T00:00:00Z"}],"checked_at":"2026-10-17T00:00:00Z"}`)}
	ev, err := e.Expiring()
	if err != nil ||
		ev.Kind != "expiry" || len(ev.Files) != 1 || ev.Files[0].ExpiresAt == nil || ev.Files[0].RetainUntil != nil {
		t.Fatalf("expiring = %+v, %v", ev, err)
	}
	if _, err := e.CollectionSubmission(); err == nil {
		t.Fatal("wrong event type should fail")
	}
	if _, err := e.File(); err == nil {
		t.Fatal("file.expiring is not a single file event")
	}
	e = WebhookEvent{Type: EventCollectionSubmitted, Data: json.RawMessage(`{"code":"abc","uploader":"张三","files":[{"id":1,"name":"a.pdf","size":3}],"failed":1}`)}
	sub, err := e.CollectionSubmission()
	if err != nil ||
		sub.Uploader != "张三" || sub.Files[0].Name != "a.pdf" || sub.Failed != 1 {
		t.Fatalf("submission = %+v, %v", sub, err)
	}
}
