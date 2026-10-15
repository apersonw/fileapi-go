package fileapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testClientID = "fa_test"
	testSecret   = "s3cr3t-s3cr3t"
	// testPrefix 模拟反向代理前缀：客户端 baseURL 带前缀，服务端看到的是去掉前缀后的路径
	testPrefix = "/api"
)

// fakeServer 模拟 FileApi：按服务端规则验签，路由到测试注册的处理函数；/storage/ 下模拟存储平台
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	// now 服务端时间
	now    time.Time
	routes map[string]http.HandlerFunc
	nonces map[string]bool
	calls  map[string]int
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, now: time.Now(), routes: map[string]http.HandlerFunc{}, nonces: map[string]bool{}, calls: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) client(t *testing.T, opts ...Option) *Client {
	c, err := New(f.srv.URL+testPrefix+"/", testClientID, testSecret, opts...)
	if err != nil {
		t.Fatal(err)
	}
	c.backoff = time.Millisecond
	return c
}

// handle 注册路由，key 形如 "GET /open/v1/files/1"
func (f *fakeServer) handle(key string, h http.HandlerFunc) { f.routes[key] = h }

func (f *fakeServer) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/storage/") {
		f.dispatch(w, r, r.Method+" "+r.URL.Path)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, testPrefix)
	if !ok {
		fail(w, 404, 40400, "no route")
		return
	}
	if code, msg := f.verify(r, path); code != 0 {
		fail(w, 401, code, msg)
		return
	}
	f.dispatch(w, r, r.Method+" "+path)
}

func (f *fakeServer) dispatch(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	f.calls[key]++
	h := f.routes[key]
	f.mu.Unlock()
	if h == nil {
		fail(w, 404, 40400, "no route "+key)
		return
	}
	h(w, r)
}

// verify 与服务端 HMAC 中间件一致的校验
func (f *fakeServer) verify(r *http.Request, path string) (int, string) {
	h := r.Header
	id, ts, nonce, hash, sig := h.Get(headerClientID), h.Get(headerTimestamp), h.Get(headerNonce), h.Get(headerContentSHA256), h.Get(headerSignature)
	if id == "" || ts == "" || nonce == "" || hash == "" || sig == "" {
		return CodeMissingAuth, "missing auth headers"
	}
	if id != testClientID {
		return CodeInvalidClient, "unknown client"
	}
	sec, _ := strconv.ParseInt(ts, 10, 64)
	f.mu.Lock()
	now := f.now
	replay := f.nonces[nonce]
	f.nonces[nonce] = true
	f.mu.Unlock()
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return CodeTimestampExpired, "timestamp expired"
	}
	if replay {
		return CodeNonceReplay, "nonce replay"
	}
	if hash == unsignedPayload {
		if !strings.HasPrefix(h.Get("Content-Type"), "multipart/form-data") {
			return CodeSignature, "UNSIGNED-PAYLOAD only for multipart"
		}
	} else {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if hashHex(body) != hash {
			return CodeSignature, "body hash mismatch"
		}
	}
	if sign(testSecret, stringToSign(r.Method, path, r.URL.Query(), id, ts, nonce, hash)) != sig {
		return CodeSignature, "bad signature"
	}
	return 0, ""
}

func (f *fakeServer) nowMillis() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now.UnixMilli()
}

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data, "request_id": "req-1", "timestamp": time.Now().UnixMilli()})
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	failAt(w, status, code, msg, time.Now().UnixMilli())
}

func failAt(w http.ResponseWriter, status, code int, msg string, ts int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": msg, "request_id": "req-1", "timestamp": ts})
}

func readJSON(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decode request body: %v", err)
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeObjects 模拟存储平台：单次 PUT 与分片 PUT
type fakeObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	parts   map[int][]byte
	// failOnce 这些分片第一次上传返回 503
	failOnce map[int]bool
}

func (f *fakeServer) storage() *fakeObjects {
	o := &fakeObjects{objects: map[string][]byte{}, parts: map[int][]byte{}, failOnce: map[int]bool{}}
	put := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Signed") != "yes" {
			http.Error(w, "missing signed header", 403)
			return
		}
		if want := r.Header.Get("X-Want-Length"); want != "" && want != strconv.FormatInt(r.ContentLength, 10) {
			http.Error(w, "content-length mismatch", 403)
			return
		}
		body, _ := io.ReadAll(r.Body)
		o.mu.Lock()
		defer o.mu.Unlock()
		if n, err := strconv.Atoi(r.URL.Query().Get("part")); err == nil {
			if o.failOnce[n] {
				delete(o.failOnce, n)
				http.Error(w, "try again", 503)
				return
			}
			o.parts[n] = body
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
			return
		}
		o.objects[r.URL.Path] = body
		w.Header().Set("ETag", `"etag"`)
	}
	f.handle("PUT /storage/obj", put)
	return o
}

func (f *fakeServer) presigned(path string, size int64, extra string) map[string]any {
	return map[string]any{
		"url": f.srv.URL + path + extra, "method": "PUT", "expires_at": time.Now().Add(time.Hour),
		"headers": map[string]string{"X-Signed": "yes", "Content-Length": strconv.FormatInt(size, 10), "X-Want-Length": strconv.FormatInt(size, 10)},
	}
}
