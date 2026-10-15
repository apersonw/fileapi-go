package fileapi

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestResumableMultipartUpload(t *testing.T) {
	f := newFakeServer(t)
	const partSize, count = 1000, 7
	content := bytes.Repeat([]byte("0123456789"), 650) // 6500 字节 → 7 片
	var (
		mu       sync.Mutex
		stored   = map[int][]byte{}
		failPart = 5
		puts     []int
	)
	etagOf := func(b []byte) string { sum := md5.Sum(b); return `"` + hex.EncodeToString(sum[:]) + `"` }
	sizeOf := func(n int) int64 {
		if n == count {
			return int64(len(content)) - partSize*(count-1)
		}
		return partSize
	}
	f.handle("PUT /storage/resume", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("part"))
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		puts = append(puts, n)
		if n == failPart {
			http.Error(w, "InvalidPart", 400)
			return
		}
		stored[n] = body
		w.Header().Set("ETag", etagOf(body))
	})
	part := func(n int) map[string]any {
		p := f.presigned("/storage/resume", sizeOf(n), "?part="+strconv.Itoa(n))
		p["part_number"], p["size"] = n, sizeOf(n)
		return p
	}
	f.handle("POST /open/v1/files/multipart", func(w http.ResponseWriter, r *http.Request) {
		parts := make([]any, count)
		for i := range parts {
			parts[i] = part(i + 1)
		}
		ok(w, map[string]any{"file": map[string]any{"id": 9, "size": len(content)}, "part_size": partSize, "part_count": count, "parts": parts})
	})
	f.handle("POST /open/v1/files/9/multipart/parts", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PartNumbers []int `json:"part_numbers"`
		}
		readJSON(t, r, &req)
		out := make([]any, len(req.PartNumbers))
		for i, n := range req.PartNumbers {
			out[i] = part(n)
		}
		ok(w, out)
	})
	f.handle("GET /open/v1/files/9/multipart", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var uploaded []any
		for n := 1; n <= count; n++ {
			if b, ok := stored[n]; ok {
				uploaded = append(uploaded, map[string]any{"part_number": n, "size": len(b), "etag": etagOf(b)})
			}
		}
		ok(w, map[string]any{"file": map[string]any{"id": 9, "size": len(content)}, "part_size": partSize, "part_count": count,
			"uploaded": uploaded, "expires_at": time.Now().Add(time.Hour)})
	})
	f.handle("POST /open/v1/files/9/multipart/complete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Parts []struct {
				PartNumber int    `json:"part_number"`
				ETag       string `json:"etag"`
			} `json:"parts"`
		}
		readJSON(t, r, &req)
		mu.Lock()
		defer mu.Unlock()
		var joined []byte
		for i, p := range req.Parts {
			if p.PartNumber != i+1 || p.ETag != etagOf(stored[p.PartNumber]) {
				fail(w, 400, CodeBadRequest, "bad part "+strconv.Itoa(i+1))
				return
			}
			joined = append(joined, stored[p.PartNumber]...)
		}
		if !bytes.Equal(joined, content) {
			fail(w, 400, CodeBadRequest, "assembled content mismatch")
			return
		}
		ok(w, map[string]any{"id": 9, "status": 1, "size": len(joined)})
	})
	c := f.client(t)
	ctx := context.Background()

	// Resumable：分片 5 失败，保留上传（不删除记录），错误带文件 ID
	_, err := c.UploadDirect(ctx, "big.bin", bytes.NewReader(content), int64(len(content)),
		&DirectUploadOptions{MultipartThreshold: 100, PartSize: partSize, Concurrency: 1, Resumable: true})
	var re *ResumableError
	if !errors.As(err, &re) || re.FileID != 9 {
		t.Fatalf("expected resumable error, got %v", err)
	}
	var se *StorageError
	if !errors.As(err, &se) || se.StatusCode != 400 {
		t.Fatalf("resumable error should wrap the storage error: %v", err)
	}
	if f.count("DELETE /open/v1/files/9") != 0 {
		t.Fatal("resumable upload was discarded")
	}

	// 存储上分片 3 的内容与本地不一致（MD5 不符）：续传时重新上传
	mu.Lock()
	stored[3] = bytes.Repeat([]byte("x"), partSize)
	puts, failPart = nil, 0
	mu.Unlock()
	st, err := c.MultipartStatus(ctx, 9)
	if err != nil || st.PartCount != count || len(st.Uploaded) != 4 || st.Uploaded[3].PartNumber != 4 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := c.ResumeUpload(ctx, 9, bytes.NewReader(content[:100]), 100, nil); err == nil {
		t.Fatal("size mismatch accepted")
	}
	var last int64
	file, err := c.ResumeUpload(ctx, 9, bytes.NewReader(content), int64(len(content)), &DirectUploadOptions{
		Concurrency: 2, Progress: func(sent, total int64) { last = sent },
	})
	if err != nil || file.Status != FileStatusActive || last != int64(len(content)) {
		t.Fatalf("resume = %+v, %v (progress %d)", file, err, last)
	}
	slices.Sort(puts)
	if !slices.Equal(puts, []int{3, 5, 6, 7}) {
		t.Fatalf("resume uploaded parts %v, want [3 5 6 7]", puts)
	}

	// 非 MD5 的 ETag（如加密存储）只比较大小
	if ok, _ := partMatches(bytes.NewReader([]byte("abc")), `"abc-2"`); !ok {
		t.Fatal("non-md5 etag should be trusted")
	}
	if ok, _ := partMatches(bytes.NewReader([]byte("abc")), etagOf([]byte("abd"))); ok {
		t.Fatal("md5 mismatch accepted")
	}
}
