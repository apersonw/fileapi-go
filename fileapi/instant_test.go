package fileapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUploadInstant(t *testing.T) {
	f := newFakeServer(t)
	content := []byte("instant content")
	f.handle("POST /open/v1/files/instant", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["filename"] != "a.txt" || req["size"] != float64(len(content)) || req["sha256"] != sum(content) ||
			req["object_type"] != "doc" || req["replace_existing"] != true {
			t.Errorf("instant request %v", req)
		}
		ok(w, map[string]any{"hit": true, "reused": false, "file": map[string]any{"id": 9, "status": 1}})
	})
	c := f.client(t)
	res, err := c.UploadInstant(context.Background(), "a.txt", sum(content), int64(len(content)),
		&InstantOptions{ObjectType: "doc", ObjectID: "1", ReplaceExisting: true})
	if err != nil || !res.Hit || res.File == nil || res.File.ID != 9 {
		t.Fatalf("instant = %+v, %v", res, err)
	}
	if _, err := c.UploadInstant(context.Background(), "a.txt", "abc", 1, nil); err == nil {
		t.Fatal("expected validation error for a bad digest")
	}
}

func TestUploadTryInstant(t *testing.T) {
	content := []byte("try instant content")
	t.Run("proxy hit skips upload", func(t *testing.T) {
		f := newFakeServer(t)
		f.handle("POST /open/v1/files/instant", func(w http.ResponseWriter, r *http.Request) {
			ok(w, map[string]any{"hit": true, "file": map[string]any{"id": 1}})
		})
		var sent int64
		res, err := f.client(t).Upload(context.Background(), "a.txt", bytes.NewReader(content), int64(len(content)),
			&UploadOptions{TryInstant: true, Progress: func(s, _ int64) { sent = s }})
		if err != nil || !res.Instant || res.ID != 1 || sent != int64(len(content)) || f.count("POST /open/v1/files") != 0 {
			t.Fatalf("result %+v, sent=%d, uploads=%d, err=%v", res, sent, f.count("POST /open/v1/files"), err)
		}
	})
	t.Run("proxy miss uploads from the original position", func(t *testing.T) {
		f := newFakeServer(t)
		f.handle("POST /open/v1/files/instant", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"hit": false}) })
		f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			_, _ = got.ReadFrom(file)
			if got.String() != string(content[4:]) {
				t.Errorf("uploaded %q", got.String())
			}
			ok(w, map[string]any{"id": 2, "deduplicated": true})
		})
		r := bytes.NewReader(content)
		_, _ = r.Seek(4, 0)
		res, err := f.client(t).Upload(context.Background(), "a.txt", r, int64(len(content)-4), &UploadOptions{TryInstant: true})
		if err != nil || res.Instant || !res.Deduplicated || res.ID != 2 {
			t.Fatalf("result %+v, %v", res, err)
		}
	})
	t.Run("proxy requires a seeker", func(t *testing.T) {
		f := newFakeServer(t)
		_, err := f.client(t).Upload(context.Background(), "a.txt", struct{ io.Reader }{strings.NewReader("x")}, 1,
			&UploadOptions{TryInstant: true})
		if err == nil || f.count("POST /open/v1/files") != 0 {
			t.Fatalf("expected error for a non-seekable reader, got %v", err)
		}
	})
	t.Run("direct hit skips presign", func(t *testing.T) {
		f := newFakeServer(t)
		f.handle("POST /open/v1/files/instant", func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			readJSON(t, r, &req)
			if req["replace_existing"] != true {
				t.Errorf("direct instant should always create a new file: %v", req)
			}
			ok(w, map[string]any{"hit": true, "file": map[string]any{"id": 3}})
		})
		file, err := f.client(t).UploadDirect(context.Background(), "a.txt", bytes.NewReader(content), int64(len(content)),
			&DirectUploadOptions{TryInstant: true})
		if err != nil || file.ID != 3 || f.count("POST /open/v1/files/presign-upload") != 0 {
			t.Fatalf("file %+v, %v", file, err)
		}
	})
}
