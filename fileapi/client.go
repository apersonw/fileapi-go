// Package fileapi 是 FileApi Open API 的 Go 客户端：请求签名、代理上传、直传与分片直传、游标列表、下载与访问链接。
//
//	c, err := fileapi.New("https://files.example.com/api", os.Getenv("FILEAPI_CLIENT_ID"), os.Getenv("FILEAPI_CLIENT_SECRET"))
//	res, err := c.UploadFile(ctx, "./photo.jpg", nil)
//	link, err := c.AccessURL(ctx, res.ID, nil)
//
// Client 可并发使用。只依赖标准库。
package fileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Version SDK 版本
const Version = "0.1.0"

// Client FileApi Open API 客户端
type Client struct {
	base     *url.URL
	clientID string
	secret   string
	hc       *http.Client
	ua       string
	retries  int
	backoff  time.Duration
	clock    func() time.Time
	// skew 本地时钟与服务端的偏差（纳秒），时间戳超窗时按服务端时间校准
	skew atomic.Int64
}

// Option 客户端选项
type Option func(*Client)

// WithHTTPClient 使用自定义 http.Client（代理、TLS、连接池等）。
// 不要设置整体 Timeout：大文件上传下载可能超过它，请用 ctx 控制单次调用的超时。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithUserAgent 设置 User-Agent
func WithUserAgent(ua string) Option { return func(c *Client) { c.ua = ua } }

// WithRetries 幂等请求（GET 与请求体可重放的查询）在网络错误、429、502、503、504 时的重试次数，默认 2；0 表示不重试。
// 每次重试都重新签名（新的时间戳与 Nonce）。上传等流式请求不重试。
func WithRetries(n int) Option { return func(c *Client) { c.retries = max(n, 0) } }

// New 创建客户端。baseURL 为服务地址，可带反向代理前缀（如 https://example.com/api，前缀不参与签名）。
func New(baseURL, clientID, clientSecret string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("fileapi: invalid base url %q", baseURL)
	}
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("fileapi: client id and secret are required")
	}
	c := &Client{
		base: u, clientID: clientID, secret: clientSecret,
		hc: &http.Client{Transport: http.DefaultTransport}, ua: "fileapi-go/" + Version,
		retries: 2, backoff: 200 * time.Millisecond, clock: time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// request 一次 Open API 请求
type request struct {
	method string
	path   string // 服务端路径，如 /open/v1/files
	query  url.Values
	// json 可重放的 JSON 请求体
	json []byte
	// stream 流式请求体（不重试），配合 contentType / contentLength（-1 表示未知）
	stream        io.Reader
	contentType   string
	contentLength int64
	header        http.Header
	idempotent    bool
}

func (c *Client) newHTTPRequest(ctx context.Context, r *request) (*http.Request, error) {
	u := *c.base
	u.Path = c.base.Path + r.path
	u.RawQuery = r.query.Encode()
	var body io.Reader
	payloadHash := emptySHA256
	switch {
	case r.stream != nil:
		body, payloadHash = r.stream, unsignedPayload
	case r.json != nil:
		body, payloadHash = bytes.NewReader(r.json), hashHex(r.json)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, err
	}
	switch {
	case r.stream != nil:
		req.Header.Set("Content-Type", r.contentType)
		if r.contentLength >= 0 {
			req.ContentLength = r.contentLength
		}
	case r.json != nil:
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.header {
		req.Header[k] = v
	}
	ts := strconv.FormatInt(c.now().Unix(), 10)
	nonce := newNonce()
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set(headerClientID, c.clientID)
	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerContentSHA256, payloadHash)
	req.Header.Set(headerSignature, sign(c.secret, stringToSign(r.method, r.path, r.query, c.clientID, ts, nonce, payloadHash)))
	return req, nil
}

func (c *Client) now() time.Time { return c.clock().Add(time.Duration(c.skew.Load())) }

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// maxRetryAfter 服务端 Retry-After 的等待上限
const maxRetryAfter = 30 * time.Second

// send 发送请求：幂等请求按配置重试，每次重新签名；时间戳超窗时校准时钟后立即重试一次（请求体可重放时）
func (c *Client) send(ctx context.Context, r *request) (*http.Response, error) {
	replayable := r.stream == nil
	attempts := 1
	if r.idempotent && replayable {
		attempts += c.retries
	}
	var (
		lastErr    error
		wait       time.Duration
		clockFixed bool
	)
	for i := 0; i < attempts; i++ {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		wait = c.backoff << i
		req, err := c.newHTTPRequest(ctx, r)
		if err != nil {
			return nil, err
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized && c.syncClock(resp) && replayable && !clockFixed {
			clockFixed = true
			resp.Body.Close()
			attempts, wait = attempts+1, 0
			continue
		}
		if retryableStatus(resp.StatusCode) && i < attempts-1 {
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				wait = min(max(wait, time.Duration(s)*time.Second), maxRetryAfter)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("fileapi: %s %s: %w", r.method, r.path, lastErr)
}

// syncClock 响应为时间戳超窗（40103）时按响应中的服务端时间校准时钟偏差，返回是否校准。
// 读取过的响应体会原样放回。
func (c *Client) syncClock(resp *http.Response) bool {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	var env struct {
		Code      int   `json:"code"`
		Timestamp int64 `json:"timestamp"`
	}
	if json.Unmarshal(b, &env) != nil || env.Code != CodeTimestampExpired || env.Timestamp <= 0 {
		return false
	}
	c.skew.Store(int64(time.UnixMilli(env.Timestamp).Sub(c.clock())))
	return true
}

// envelope 统一响应体
type envelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
}

// call 发送请求并把 data 解析到 out（out 为 nil 时忽略）
func (c *Client) call(ctx context.Context, r *request, out any) error {
	resp, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("fileapi: read response: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		if resp.StatusCode >= 400 {
			return &Error{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(truncate(b, 256)))}
		}
		return fmt.Errorf("fileapi: invalid response (http %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 || env.Code != 0 {
		return &Error{StatusCode: resp.StatusCode, Code: env.Code, Message: env.Message, RequestID: env.RequestID}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("fileapi: decode data: %w", err)
	}
	return nil
}

// decodeError 非 2xx 的下载等响应转换为 *Error
func decodeError(resp *http.Response) error {
	defer resp.Body.Close()
	return decode(resp, nil)
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
