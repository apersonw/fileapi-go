package fileapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestScanFieldsAndErrors(t *testing.T) {
	f := newFakeServer(t)
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("object_id") == "virus" {
			fail(w, http.StatusUnprocessableEntity, CodeFileInfected, "文件未通过病毒扫描：Eicar-Test-Signature")
			return
		}
		ok(w, map[string]any{"id": 1, "scan_status": "clean"})
	})
	f.handle("GET /open/v1/files/2", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"id": 2, "scan_status": "pending"})
	})
	c := f.client(t)
	ctx := context.Background()
	res, err := c.Upload(ctx, "a.txt", bytes.NewReader([]byte("ok")), 2, nil)
	if err != nil || res.ScanStatus != ScanClean {
		t.Fatalf("upload = %+v, %v", res, err)
	}
	_, err = c.Upload(ctx, "v.txt", bytes.NewReader([]byte("x")), 1, &UploadOptions{ObjectType: "t", ObjectID: "virus"})
	if !IsCode(err, CodeFileInfected) {
		t.Fatalf("infected upload err = %v", err)
	}
	got, err := c.Get(ctx, 2)
	if err != nil || got.ScanStatus != ScanPending {
		t.Fatalf("get = %+v, %v", got, err)
	}
}
