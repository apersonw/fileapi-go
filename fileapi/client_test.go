package fileapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewValidates(t *testing.T) {
	for _, base := range []string{"", "ftp://x", "http://", "::"} {
		if _, err := New(base, "id", "secret"); err == nil {
			t.Errorf("New(%q) should fail", base)
		}
	}
	if _, err := New("http://x", "", "secret"); err == nil {
		t.Error("empty client id should fail")
	}
}

func TestGetSignsQueryAndPathBehindPrefix(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/42", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"id": 42, "original_filename": "报告 v2.pdf", "size": 10, "status": 1, "metadata": map[string]string{"k": "v"}})
	})
	file, err := f.client(t).Get(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if file.ID != 42 || file.OriginalFilename != "报告 v2.pdf" || file.Metadata["k"] != "v" {
		t.Fatalf("unexpected file %+v", file)
	}
}

func TestErrorsAreTyped(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/1", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, CodeNotFound, "文件不存在") })
	f.handle("GET /open/v1/files/2", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "<html>bad gateway</html>", 500) })
	c := f.client(t)

	_, err := c.Get(context.Background(), 1)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.Code != CodeNotFound || apiErr.RequestID != "req-1" || !IsNotFound(err) {
		t.Fatalf("unexpected error %#v", err)
	}
	_, err = c.Get(context.Background(), 2)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || !strings.Contains(apiErr.Message, "bad gateway") {
		t.Fatalf("non-JSON error not surfaced: %#v", err)
	}

	bad, _ := New(f.srv.URL+testPrefix, testClientID, "wrong")
	if _, err := bad.Get(context.Background(), 1); !IsCode(err, CodeSignature) {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestRetriesResignIdempotentRequests(t *testing.T) {
	f := newFakeServer(t)
	var mu sync.Mutex
	var nonces []string
	f.handle("GET /open/v1/files/7", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		nonces = append(nonces, r.Header.Get(headerNonce))
		n := len(nonces)
		mu.Unlock()
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			fail(w, 503, 50002, "busy")
			return
		}
		ok(w, map[string]any{"id": 7})
	})
	f.handle("DELETE /open/v1/files/7", func(w http.ResponseWriter, r *http.Request) { fail(w, 503, 50002, "busy") })
	c := f.client(t)
	if _, err := c.Get(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(nonces) != 3 || nonces[0] == nonces[1] || nonces[1] == nonces[2] {
		t.Fatalf("expected 3 attempts with fresh nonces, got %v", nonces)
	}
	// 非幂等请求不重试
	if err := c.Delete(context.Background(), 7); !IsCode(err, 50002) || f.count("DELETE /open/v1/files/7") != 1 {
		t.Fatalf("delete retried or error lost: %v, calls=%d", err, f.count("DELETE /open/v1/files/7"))
	}
	// 关闭重试
	nonces = nil
	if _, err := f.client(t, WithRetries(0)).Get(context.Background(), 7); !IsCode(err, 50002) || len(nonces) != 1 {
		t.Fatalf("WithRetries(0): %v, attempts=%d", err, len(nonces))
	}
}

func TestClockSkewIsCorrected(t *testing.T) {
	f := newFakeServer(t)
	f.now = time.Now().Add(2 * time.Hour) // 本地时钟慢了两小时
	f.handle("GET /open/v1/files/1", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 1}) })
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 超窗响应携带服务端时间
		sec, _ := strconv.ParseInt(r.Header.Get(headerTimestamp), 10, 64)
		if d := f.now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
			failAt(w, 401, CodeTimestampExpired, "timestamp expired", f.nowMillis())
			return
		}
		f.serve(w, r)
	})
	c := f.client(t, WithRetries(0))
	if _, err := c.Get(context.Background(), 1); err != nil {
		t.Fatalf("first call should succeed after clock sync: %v", err)
	}
	if d := time.Duration(c.skew.Load()); d < 119*time.Minute || d > 121*time.Minute {
		t.Fatalf("skew = %v, want ~2h", d)
	}
	if _, err := c.Get(context.Background(), 1); err != nil || f.count("GET /open/v1/files/1") != 2 {
		t.Fatalf("second call: %v (calls %d)", err, f.count("GET /open/v1/files/1"))
	}
}

func TestListQueryAndListAll(t *testing.T) {
	f := newFakeServer(t)
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f.handle("GET /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("category") != "IMAGE" || q.Get("original_filename") != "a b" || q.Get("start_time") != "2026-01-02T03:04:05Z" ||
			q.Get("sort") != "size" || q.Get("page_size") != "2" || q.Has("page") || q.Has("with_total") {
			t.Errorf("unexpected query %v", q)
		}
		switch q.Get("cursor") {
		case "":
			ok(w, map[string]any{"list": []map[string]any{{"id": 1}, {"id": 2}}, "page_size": 2, "has_more": true, "next_cursor": "c1"})
		case "c1":
			ok(w, map[string]any{"list": []map[string]any{{"id": 3}}, "page_size": 2, "has_more": false})
		default:
			t.Errorf("unexpected cursor %q", q.Get("cursor"))
		}
	})
	c := f.client(t)
	q := &ListQuery{Category: CategoryImage, OriginalFilename: "a b", StartTime: start, Sort: SortSize, PageSize: 2, Page: 3, WithTotal: true}
	var ids []int64
	for file, err := range c.ListAll(context.Background(), q) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, file.ID)
	}
	if fmt.Sprint(ids) != "[1 2 3]" {
		t.Fatalf("ids = %v", ids)
	}
	// 提前结束遍历不再请求下一页
	before := f.count("GET /open/v1/files")
	for range c.ListAll(context.Background(), q) {
		break
	}
	if f.count("GET /open/v1/files") != before+1 {
		t.Fatal("ListAll fetched more pages after break")
	}
}

func TestBatchCallsAreChunked(t *testing.T) {
	f := newFakeServer(t)
	f.handle("POST /open/v1/files/batch-get", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ IDs []int64 }
		readJSON(t, r, &req)
		if len(req.IDs) > maxBatchIDs {
			t.Errorf("batch of %d ids", len(req.IDs))
		}
		list := make([]map[string]any, len(req.IDs))
		for i, id := range req.IDs {
			list[i] = map[string]any{"id": id}
		}
		ok(w, list)
	})
	f.handle("POST /open/v1/files/batch-delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ IDs []int64 }
		readJSON(t, r, &req)
		ok(w, map[string]any{"deleted": req.IDs[1:], "failed": map[string]string{strconv.FormatInt(req.IDs[0], 10): "文件不存在"}})
	})
	ids := make([]int64, 250)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	c := f.client(t)
	files, err := c.BatchGet(context.Background(), ids)
	if err != nil || len(files) != 250 || f.count("POST /open/v1/files/batch-get") != 3 {
		t.Fatalf("batch get: %d files, %v, calls=%d", len(files), err, f.count("POST /open/v1/files/batch-get"))
	}
	res, err := c.BatchDelete(context.Background(), ids)
	if err != nil || len(res.Deleted) != 247 || len(res.Failed) != 3 || res.Failed[101] == "" {
		t.Fatalf("batch delete: %+v %v", res, err)
	}
	if files, err := c.BatchGet(context.Background(), nil); err != nil || len(files) != 0 {
		t.Fatal("empty batch should not call the server")
	}
}

func TestUploadStreamsWithChecksum(t *testing.T) {
	f := newFakeServer(t)
	content := bytes.Repeat([]byte("0123456789"), 100_000) // 1MB
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		wantLen := r.Header.Get("X-Test-Length")
		if (wantLen == "known") != (r.ContentLength > 0) {
			t.Errorf("content length %d, expected %s", r.ContentLength, wantLen)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse form: %v", err)
			return
		}
		file, fh, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			return
		}
		data, _ := io.ReadAll(file)
		if r.FormValue("sha256") != sum(data) {
			fail(w, 400, CodeBadRequest, "文件 SHA-256 校验失败")
			return
		}
		if r.FormValue("object_type") != "order" || r.FormValue("replace_existing") != "true" || r.FormValue("metadata") != `{"k":"v"}` {
			t.Errorf("fields: %v", r.MultipartForm.Value)
		}
		ok(w, map[string]any{"id": 9, "original_filename": fh.Filename, "size": len(data), "sha256": sum(data), "reused": false})
	})
	c := f.client(t)
	opts := &UploadOptions{ObjectType: "order", ObjectID: "1", ReplaceExisting: true, Metadata: map[string]string{"k": "v"}}

	// 长度已知：设置 Content-Length，长度必须与实际发送的完全一致
	var last, total int64
	opts.Progress = func(sent, t int64) { last, total = sent, t }
	c.hc.Transport = headerTransport{"X-Test-Length": "known"}
	res, err := c.Upload(context.Background(), `季度 "报告".txt`, bytes.NewReader(content), int64(len(content)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != 9 || res.OriginalFilename != `季度 "报告".txt` || res.Size != int64(len(content)) || last != int64(len(content)) || total != last {
		t.Fatalf("result %+v progress %d/%d", res, last, total)
	}

	// 长度未知：分块编码
	c.hc.Transport = headerTransport{"X-Test-Length": "unknown"}
	opts.Progress = nil
	if _, err := c.Upload(context.Background(), "a.txt", io.MultiReader(bytes.NewReader(content)), -1, opts); err != nil {
		t.Fatal(err)
	}

	// 本地文件
	c.hc.Transport = headerTransport{"X-Test-Length": "known"}
	path := filepath.Join(t.TempDir(), "local.bin")
	if err := os.WriteFile(path, content[:1234], 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := c.UploadFile(context.Background(), path, opts); err != nil || res.OriginalFilename != "local.bin" || res.Size != 1234 {
		t.Fatalf("upload file: %+v %v", res, err)
	}
	if _, err := c.UploadFile(context.Background(), t.TempDir(), opts); err == nil {
		t.Fatal("uploading a directory should fail")
	}

	// 读取的内容少于声明的长度：请求失败而不是上传半截文件
	if _, err := c.Upload(context.Background(), "short.txt", bytes.NewReader(content[:10]), 100, opts); err == nil {
		t.Fatal("short body should fail")
	}
}

// headerTransport 给请求加测试头，便于服务端断言
type headerTransport map[string]string

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestUploadDirectSingle(t *testing.T) {
	f := newFakeServer(t)
	store := f.storage()
	content := []byte("hello direct upload")
	completed := 0
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["filename"] != "d.txt" || req["size"] != float64(len(content)) || req["content_type"] != "text/plain" {
			t.Errorf("presign request %v", req)
		}
		ok(w, map[string]any{"file": map[string]any{"id": 5, "status": 0}, "upload": f.presigned("/storage/obj", int64(len(content)), "")})
	})
	f.handle("POST /open/v1/files/5/complete", func(w http.ResponseWriter, r *http.Request) {
		completed++
		ok(w, map[string]any{"id": 5, "status": 1, "size": len(content)})
	})
	var sent int64
	file, err := f.client(t).UploadDirect(context.Background(), "d.txt", bytes.NewReader(content), int64(len(content)),
		&DirectUploadOptions{ContentType: "text/plain", Progress: func(s, _ int64) { sent = s }})
	if err != nil {
		t.Fatal(err)
	}
	if file.Status != FileStatusActive || completed != 1 || !bytes.Equal(store.objects["/storage/obj"], content) || sent != int64(len(content)) {
		t.Fatalf("file %+v completed=%d stored=%q sent=%d", file, completed, store.objects["/storage/obj"], sent)
	}
}

func TestUploadDirectStorageRejectDiscardsRecord(t *testing.T) {
	f := newFakeServer(t)
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		up := f.presigned("/storage/denied", 3, "")
		ok(w, map[string]any{"file": map[string]any{"id": 6}, "upload": up})
	})
	f.handle("PUT /storage/denied", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "<Error>AccessDenied</Error>", 403) })
	f.handle("DELETE /open/v1/files/6", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	_, err := f.client(t).UploadDirect(context.Background(), "x.txt", bytes.NewReader([]byte("abc")), 3, nil)
	var se *StorageError
	if !errors.As(err, &se) || se.StatusCode != 403 || !strings.Contains(se.Body, "AccessDenied") {
		t.Fatalf("expected storage error, got %v", err)
	}
	if f.count("DELETE /open/v1/files/6") != 1 {
		t.Fatal("pending record not discarded")
	}
}

func TestUploadDirectMultipart(t *testing.T) {
	f := newFakeServer(t)
	store := f.storage()
	store.failOnce[3] = true // 分片 3 第一次失败，重试时重新签发地址
	const partSize, count = 1000, 7
	content := bytes.Repeat([]byte("abcdefghij"), 650) // 6500 字节 → 7 个分片，最后一个 500 字节
	part := func(n int) map[string]any {
		size := int64(partSize)
		if n == count {
			size = int64(len(content)) - partSize*(count-1)
		}
		p := f.presigned("/storage/obj", size, "?part="+strconv.Itoa(n))
		p["part_number"], p["size"] = n, size
		if n == 2 {
			p["expires_at"] = time.Now().Add(10 * time.Second) // 即将过期，应重新签发
		}
		return p
	}
	var mu sync.Mutex
	var presignedFor []int
	f.handle("POST /open/v1/files/multipart", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		readJSON(t, r, &req)
		if req["part_size"] != float64(partSize) || req["size"] != float64(len(content)) {
			t.Errorf("init request %v", req)
		}
		// 只返回前 3 个分片的地址，其余需要补签
		ok(w, map[string]any{"file": map[string]any{"id": 8}, "part_size": partSize, "part_count": count, "parts": []any{part(1), part(2), part(3)}})
	})
	f.handle("POST /open/v1/files/8/multipart/parts", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PartNumbers []int `json:"part_numbers"`
		}
		readJSON(t, r, &req)
		mu.Lock()
		presignedFor = append(presignedFor, req.PartNumbers...)
		mu.Unlock()
		out := make([]any, len(req.PartNumbers))
		for i, n := range req.PartNumbers {
			out[i] = part(n)
			if n == 2 {
				out[i].(map[string]any)["expires_at"] = time.Now().Add(time.Hour)
			}
		}
		ok(w, out)
	})
	f.handle("POST /open/v1/files/8/multipart/complete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Parts []struct {
				PartNumber int    `json:"part_number"`
				ETag       string `json:"etag"`
			} `json:"parts"`
		}
		readJSON(t, r, &req)
		var joined []byte
		for i, p := range req.Parts {
			if p.PartNumber != i+1 || p.ETag != fmt.Sprintf(`"etag-%d"`, i+1) {
				t.Errorf("part %d: %+v", i+1, p)
			}
			joined = append(joined, store.parts[p.PartNumber]...)
		}
		if !bytes.Equal(joined, content) {
			fail(w, 400, CodeBadRequest, "assembled content mismatch")
			return
		}
		ok(w, map[string]any{"id": 8, "status": 1, "size": len(joined)})
	})

	var mu2 sync.Mutex
	var maxSent int64
	file, err := f.client(t).UploadDirect(context.Background(), "big.bin", bytes.NewReader(content), int64(len(content)), &DirectUploadOptions{
		MultipartThreshold: 100, PartSize: partSize, Concurrency: 3,
		Progress: func(sent, total int64) {
			mu2.Lock()
			maxSent = max(maxSent, sent)
			mu2.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if file.ID != 8 || file.Status != FileStatusActive || maxSent != int64(len(content)) {
		t.Fatalf("file %+v progress %d", file, maxSent)
	}
	got := map[int]bool{}
	for _, n := range presignedFor {
		got[n] = true
	}
	for _, n := range []int{2, 3, 4, 5, 6, 7} {
		if !got[n] {
			t.Errorf("part %d was not re-presigned (presigned: %v)", n, presignedFor)
		}
	}
}

func TestUploadDirectMultipartFailureCancels(t *testing.T) {
	f := newFakeServer(t)
	f.handle("POST /open/v1/files/multipart", func(w http.ResponseWriter, r *http.Request) {
		parts := make([]any, 5)
		for i := range parts {
			p := f.presigned("/storage/bad", 10, "?part="+strconv.Itoa(i+1))
			p["part_number"], p["size"] = i+1, 10
			parts[i] = p
		}
		ok(w, map[string]any{"file": map[string]any{"id": 11}, "part_size": 10, "part_count": 5, "parts": parts})
	})
	f.handle("PUT /storage/bad", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "EntityTooSmall", 400) })
	f.handle("DELETE /open/v1/files/11", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	_, err := f.client(t).UploadDirect(context.Background(), "x.bin", bytes.NewReader(make([]byte, 50)), 50,
		&DirectUploadOptions{MultipartThreshold: 1, Concurrency: 2})
	var se *StorageError
	if !errors.As(err, &se) || se.StatusCode != 400 {
		t.Fatalf("expected storage error, got %v", err)
	}
	if n := f.count("PUT /storage/bad"); n > 2 {
		t.Fatalf("4xx should not be retried nor keep uploading other parts: %d PUTs", n)
	}
	if f.count("DELETE /open/v1/files/11") != 1 {
		t.Fatal("pending record not discarded")
	}
}

func TestDownloadAndRange(t *testing.T) {
	f := newFakeServer(t)
	content := []byte("0123456789abcdef")
	f.handle("GET /open/v1/files/3/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="____.txt"; filename*=UTF-8''%E6%8A%A5%E5%91%8A.txt`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
	})
	f.handle("GET /open/v1/files/4/download", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, CodeNotFound, "文件不存在") })
	c := f.client(t)

	obj, err := c.Open(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(obj)
	obj.Close()
	if !bytes.Equal(data, content) || obj.Filename != "报告.txt" || obj.Size != 16 || obj.ContentType != "text/plain" {
		t.Fatalf("object %+v data %q", obj, data)
	}

	obj, err = c.OpenRange(context.Background(), 3, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(obj)
	obj.Close()
	if string(data) != "abcd" || obj.ContentLength != 4 || obj.Size != 16 {
		t.Fatalf("range object %+v data %q", obj, data)
	}
	obj, err = c.OpenRange(context.Background(), 3, 12, -1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(obj)
	obj.Close()
	if string(data) != "cdef" {
		t.Fatalf("open-ended range %q", data)
	}

	path := filepath.Join(t.TempDir(), "out.txt")
	if err := c.DownloadFile(context.Background(), 3, path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatalf("downloaded %q", got)
	}
	if err := c.DownloadFile(context.Background(), 4, filepath.Join(t.TempDir(), "missing.txt")); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestAccessURLOptions(t *testing.T) {
	f := newFakeServer(t)
	var last string
	f.handle("GET /open/v1/files/1/url", func(w http.ResponseWriter, r *http.Request) {
		last = r.URL.Query().Encode()
		ok(w, map[string]any{"url": "https://files.test/a/tok", "mode": "gateway"})
	})
	c := f.client(t)
	cases := []struct {
		opts *URLOptions
		want string
	}{
		{nil, ""},
		{&URLOptions{Expires: 1500 * time.Millisecond, Download: true}, "download=true&expires=2"},
		{&URLOptions{Mode: ModeDirect, Permanent: true, Expires: time.Hour, Thumbnail: true}, "expires=0&mode=direct&thumbnail=true"},
		{&URLOptions{Image: &ImageOptions{Width: 300, Height: 200, Fit: FitCover, Format: FormatJPEG, Quality: 80}}, "fit=cover&format=jpeg&h=200&quality=80&w=300"},
		{&URLOptions{Image: &ImageOptions{Width: 64}}, "w=64"},
		{&URLOptions{Image: &ImageOptions{Width: 300, Watermark: &Watermark{Text: "仅供 A", Position: PositionTile, Opacity: 30, Size: 6, Color: "ff0000"}}},
			"w=300&wm_color=ff0000&wm_opacity=30&wm_pos=tile&wm_size=6&wm_text=%E4%BB%85%E4%BE%9B+A"},
		{&URLOptions{Image: &ImageOptions{Watermark: &Watermark{ImageID: 42}}}, "wm_image=42"},
	}
	for _, tc := range cases {
		u, err := c.AccessURL(context.Background(), 1, tc.opts)
		if err != nil || u.URL != "https://files.test/a/tok" || u.ExpiresAt != nil {
			t.Fatalf("%+v: %+v %v", tc.opts, u, err)
		}
		if last != tc.want {
			t.Errorf("%+v: query %q, want %q", tc.opts, last, tc.want)
		}
	}
}

func TestRenameResolveNamesDelete(t *testing.T) {
	f := newFakeServer(t)
	f.handle("PATCH /open/v1/files/2", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		readJSON(t, r, &req)
		ok(w, map[string]any{"id": 2, "original_filename": req["original_filename"]})
	})
	f.handle("POST /open/v1/files/resolve-names", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]string{"https://cdn.test/a.png": "a.png"})
	})
	f.handle("DELETE /open/v1/files/2", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	c := f.client(t)
	if file, err := c.Rename(context.Background(), 2, "新名字.txt"); err != nil || file.OriginalFilename != "新名字.txt" {
		t.Fatalf("rename: %+v %v", file, err)
	}
	if names, err := c.ResolveNames(context.Background(), []string{"https://cdn.test/a.png", "https://cdn.test/b.png"}); err != nil || names["https://cdn.test/a.png"] != "a.png" || len(names) != 1 {
		t.Fatalf("resolve: %v %v", names, err)
	}
	if err := c.Delete(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
}

func TestContextCancel(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/files/1", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := f.client(t).Get(ctx, 1); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("expected deadline exceeded promptly, got %v after %v", err, time.Since(start))
	}
}
