package fileapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DirectUploadOptions 直传选项
type DirectUploadOptions struct {
	// ContentType 不传时服务端按扩展名推断
	ContentType string
	ObjectType  string
	ObjectID    string
	Metadata    map[string]string
	// MultipartThreshold 文件大小达到该值时分片直传，默认 64MiB
	MultipartThreshold int64
	// PartSize 分片大小（5MiB~5GiB），默认由服务端决定；分片数超过 10000 时服务端自动放大
	PartSize int64
	// Concurrency 并发上传的分片数，默认 4
	Concurrency int
	// Progress 进度回调：已上传字节数与总字节数。分片失败重试时进度会回退；回调串行执行
	Progress func(sent, total int64)
	// TryInstant 先读一遍内容计算 SHA-256 尝试秒传（见 UploadInstant），命中时不再上传
	TryInstant bool
	// ExpiresIn 多久后过期（到期后服务端自动删除，按秒向上取整），0 表示不过期
	ExpiresIn time.Duration
	// Resumable 分片直传失败时保留未完成的上传（不删除记录），返回 *ResumableError，之后可用 ResumeUpload 从中断处继续
	Resumable bool
	// VersionOf 作为该文件的新版本上传（见 UploadOptions.VersionOf），确认完成后返回文件的最新信息
	VersionOf int64
	// Folder 所在目录，默认根目录；不能与 VersionOf 同时使用
	Folder string
	// TagIDs 文件的标签（本客户端的标签，最多 20 个）；不能与 VersionOf 同时使用
	TagIDs []int64
	// Transcode 上传完成后转码为多清晰度 HLS（见 Play）；Profiles 档位（如 "720p"），指定即视为转码，为空用服务端默认
	Transcode bool
	Profiles  []string
}

// ResumableError 分片直传中断，已上传的分片保留在存储上：用 FileID 调用 ResumeUpload 继续
// （须在服务端清理未完成上传之前，见 MultipartStatus.ExpiresAt）
type ResumableError struct {
	FileID int64
	Err    error
}

func (e *ResumableError) Error() string {
	return fmt.Sprintf("fileapi: multipart upload of file %d interrupted (resumable): %v", e.FileID, e.Err)
}

func (e *ResumableError) Unwrap() error { return e.Err }

const (
	defaultMultipartThreshold = 64 << 20
	defaultConcurrency        = 4
	// partAttempts 单个分片的最大尝试次数（每次重新获取分片地址）
	partAttempts = 3
	// presignBatch 分片地址不足时每次补签的数量
	presignBatch = 100
	// urlRefreshBefore 分片地址剩余有效期不足该值时重新签发
	urlRefreshBefore = time.Minute
)

// presigned 直传地址
type presigned struct {
	PartNumber int               `json:"part_number"`
	Size       int64             `json:"size"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers,omitempty"`
	ExpiresAt  time.Time         `json:"expires_at"`
}

// UploadDirect 直传：向 FileApi 申请预签名地址后，内容直接上传到存储平台（不经过 FileApi），完成后确认。
// 大于等于 MultipartThreshold 的文件自动分片并发上传。r 需支持并发 ReadAt（如 *os.File、*bytes.Reader）。
//
// 上传失败时 SDK 会尽量删除待确认的记录以释放配额（服务端也会定期清理未完成的直传）。
func (c *Client) UploadDirect(ctx context.Context, filename string, r io.ReaderAt, size int64, opts *DirectUploadOptions) (*File, error) {
	if filename == "" {
		return nil, errors.New("fileapi: filename is required")
	}
	if size <= 0 {
		return nil, errors.New("fileapi: direct upload requires a positive size")
	}
	if opts == nil {
		opts = &DirectUploadOptions{}
	}
	if opts.TryInstant && opts.VersionOf == 0 {
		// 直传总是登记新文件，对应秒传的 replace_existing=true
		res, err := c.tryInstant(ctx, filename, io.NewSectionReader(r, 0, size), size, &InstantOptions{
			ObjectType: opts.ObjectType, ObjectID: opts.ObjectID, ReplaceExisting: true, Metadata: opts.Metadata,
			ExpiresIn: opts.ExpiresIn, Folder: opts.Folder, TagIDs: opts.TagIDs, Transcode: opts.Transcode, Profiles: opts.Profiles,
		}, opts.Progress)
		if err != nil {
			return nil, err
		}
		if res.Hit {
			return res.File, nil
		}
	}
	threshold := opts.MultipartThreshold
	if threshold <= 0 {
		threshold = defaultMultipartThreshold
	}
	req := map[string]any{
		"filename": filename, "content_type": opts.ContentType, "size": size,
		"object_type": opts.ObjectType, "object_id": opts.ObjectID, "metadata": opts.Metadata,
	}
	if opts.Folder != "" {
		req["folder"] = opts.Folder
	}
	if len(opts.TagIDs) > 0 {
		req["tag_ids"] = opts.TagIDs
	}
	if opts.Transcode || len(opts.Profiles) > 0 {
		req["transcode"], req["profiles"] = true, opts.Profiles
	}
	if n := expiresIn(opts.ExpiresIn); n > 0 {
		req["expires_in"] = n
	}
	if opts.VersionOf > 0 {
		req["version_of"] = opts.VersionOf
	}
	prog := &progress{total: size, fn: opts.Progress}
	if size < threshold {
		return c.uploadSingle(ctx, req, r, size, prog)
	}
	if opts.PartSize > 0 {
		req["part_size"] = opts.PartSize
	}
	return c.uploadMultipart(ctx, req, r, concurrency(opts), prog, opts.Resumable)
}

func concurrency(opts *DirectUploadOptions) int {
	if opts.Concurrency > 0 {
		return opts.Concurrency
	}
	return defaultConcurrency
}

// UploadFileDirect 直传本地文件，文件名取自路径
func (c *Client) UploadFileDirect(ctx context.Context, path string, opts *DirectUploadOptions) (*File, error) {
	f, st, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return c.UploadDirect(ctx, filepath.Base(path), f, st.Size(), opts)
}

func (c *Client) uploadSingle(ctx context.Context, body map[string]any, r io.ReaderAt, size int64, prog *progress) (*File, error) {
	var res struct {
		File   File      `json:"file"`
		Upload presigned `json:"upload"`
	}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/presign-upload", json: jsonBody(body)}, &res); err != nil {
		return nil, err
	}
	id := res.File.ID
	cr := &countingReader{r: io.NewSectionReader(r, 0, size), p: prog}
	if _, err := c.putObject(ctx, &res.Upload, cr, size); err != nil {
		c.discard(ctx, id)
		return nil, err
	}
	var f File
	if err := c.call(ctx, &request{method: http.MethodPost, path: filePath(id, "/complete"), idempotent: true}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *Client) uploadMultipart(ctx context.Context, body map[string]any, r io.ReaderAt, conc int, prog *progress, resumable bool) (*File, error) {
	var init struct {
		File      File        `json:"file"`
		PartSize  int64       `json:"part_size"`
		PartCount int         `json:"part_count"`
		Parts     []presigned `json:"parts"`
	}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/multipart", json: jsonBody(body)}, &init); err != nil {
		return nil, err
	}
	id := init.File.ID
	up := &multipartUpload{c: c, id: id, count: init.PartCount, urls: map[int]presigned{}}
	for _, p := range init.Parts {
		up.urls[p.PartNumber] = p
	}
	numbers := make([]int, init.PartCount)
	for i := range numbers {
		numbers[i] = i + 1
	}
	etags := make([]string, init.PartCount)
	err := up.uploadParts(ctx, r, init.PartSize, numbers, etags, conc, prog)
	if err == nil {
		var f *File
		if f, err = c.completeMultipart(ctx, id, etags); err == nil {
			return f, nil
		}
	}
	if resumable {
		return nil, &ResumableError{FileID: id, Err: err}
	}
	c.discard(ctx, id)
	return nil, err
}

// MultipartStatus 分片直传进度：已上传且大小正确的分片，用于断点续传
func (c *Client) MultipartStatus(ctx context.Context, id int64) (*MultipartStatus, error) {
	var st MultipartStatus
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/multipart"), idempotent: true}, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ResumeUpload 从中断处继续分片直传：r 与 size 须为同一份内容。已上传的分片用其 MD5 ETag 校验本地内容
// （存储返回的 ETag 不是 MD5 时只比较大小），一致的跳过，其余重新上传后合并。
// 失败时保留已上传的分片并返回 *ResumableError，可再次调用继续；opts 只使用 Concurrency 与 Progress。
func (c *Client) ResumeUpload(ctx context.Context, id int64, r io.ReaderAt, size int64, opts *DirectUploadOptions) (*File, error) {
	if opts == nil {
		opts = &DirectUploadOptions{}
	}
	st, err := c.MultipartStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	if st.File.Size != size {
		return nil, fmt.Errorf("fileapi: resume file %d: size %d differs from the upload (%d)", id, size, st.File.Size)
	}
	prog := &progress{total: size, fn: opts.Progress}
	etags := make([]string, st.PartCount)
	for _, p := range st.Uploaded {
		if p.PartNumber < 1 || p.PartNumber > st.PartCount {
			continue
		}
		ok, err := partMatches(io.NewSectionReader(r, int64(p.PartNumber-1)*st.PartSize, p.Size), p.ETag)
		if err != nil {
			return nil, err
		}
		if ok {
			etags[p.PartNumber-1] = p.ETag
			prog.add(p.Size)
		}
	}
	var missing []int
	for i, etag := range etags {
		if etag == "" {
			missing = append(missing, i+1)
		}
	}
	up := &multipartUpload{c: c, id: id, count: st.PartCount, urls: map[int]presigned{}}
	err = up.uploadParts(ctx, r, st.PartSize, missing, etags, concurrency(opts), prog)
	if err == nil {
		var f *File
		if f, err = c.completeMultipart(ctx, id, etags); err == nil {
			return f, nil
		}
	}
	return nil, &ResumableError{FileID: id, Err: err}
}

// ResumeUploadFile 用本地文件继续分片直传
func (c *Client) ResumeUploadFile(ctx context.Context, id int64, path string, opts *DirectUploadOptions) (*File, error) {
	f, st, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return c.ResumeUpload(ctx, id, f, st.Size(), opts)
}

// partMatches 本地分片内容与存储上的 ETag 是否一致：ETag 为 32 位十六进制时按 MD5 比较，否则（如加密存储）只能信任大小
func partMatches(r io.Reader, etag string) (bool, error) {
	want := strings.ToLower(strings.Trim(etag, `"`))
	if len(want) != 32 || strings.Trim(want, "0123456789abcdef") != "" {
		return true, nil
	}
	h := md5.New()
	if _, err := io.Copy(h, r); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

// uploadParts 并发上传指定分片，etags 按分片号写入
func (m *multipartUpload) uploadParts(ctx context.Context, r io.ReaderAt, partSize int64, numbers []int, etags []string, conc int, prog *progress) error {
	if len(numbers) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(conc, len(numbers)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				offset := int64(n-1) * partSize
				etag, err := m.uploadPart(ctx, n, func(size int64) *countingReader {
					return &countingReader{r: io.NewSectionReader(r, offset, size), p: prog}
				})
				if err != nil {
					cancel(fmt.Errorf("fileapi: upload part %d: %w", n, err))
					return
				}
				etags[n-1] = etag
			}
		}()
	}
feed:
	for _, n := range numbers {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return context.Cause(ctx)
}

func (c *Client) completeMultipart(ctx context.Context, id int64, etags []string) (*File, error) {
	parts := make([]map[string]any, len(etags))
	for i, etag := range etags {
		parts[i] = map[string]any{"part_number": i + 1, "etag": etag}
	}
	var f File
	req := &request{method: http.MethodPost, path: filePath(id, "/multipart/complete"), json: jsonBody(map[string]any{"parts": parts}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// multipartUpload 分片地址缓存：初始化时服务端最多返回前 1000 个分片的地址，其余及即将过期的按需补签
type multipartUpload struct {
	c     *Client
	id    int64
	count int
	mu    sync.Mutex
	urls  map[int]presigned
}

// url 取分片地址；缺失、即将过期或 refresh 时从 n 开始批量补签
func (m *multipartUpload) url(ctx context.Context, n int, refresh bool) (presigned, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.urls[n]; ok && !refresh && time.Until(p.ExpiresAt) > urlRefreshBefore {
		return p, nil
	}
	numbers := make([]int, 0, presignBatch)
	for i := n; i <= m.count && len(numbers) < presignBatch; i++ {
		if p, ok := m.urls[i]; i == n || !ok || time.Until(p.ExpiresAt) <= urlRefreshBefore {
			numbers = append(numbers, i)
		}
	}
	var parts []presigned
	req := &request{method: http.MethodPost, path: filePath(m.id, "/multipart/parts"), json: jsonBody(map[string]any{"part_numbers": numbers}), idempotent: true}
	if err := m.c.call(ctx, req, &parts); err != nil {
		return presigned{}, err
	}
	for _, p := range parts {
		m.urls[p.PartNumber] = p
	}
	p, ok := m.urls[n]
	if !ok {
		return presigned{}, fmt.Errorf("fileapi: server returned no url for part %d", n)
	}
	return p, nil
}

func (m *multipartUpload) uploadPart(ctx context.Context, n int, body func(size int64) *countingReader) (string, error) {
	var lastErr error
	for attempt := 0; attempt < partAttempts; attempt++ {
		p, err := m.url(ctx, n, attempt > 0)
		if err != nil {
			return "", err
		}
		cr := body(p.Size)
		etag, err := m.c.putObject(ctx, &p, cr, p.Size)
		if err == nil {
			if etag == "" {
				return "", fmt.Errorf("fileapi: storage returned no ETag for part %d", n)
			}
			return etag, nil
		}
		cr.rollback()
		if ctx.Err() != nil || !retryablePut(err) {
			return "", err
		}
		lastErr = err
	}
	return "", lastErr
}

// retryablePut 网络错误、5xx、以及 403（通常是地址过期，重试时会重新签发）值得重试
func retryablePut(err error) bool {
	var se *StorageError
	if errors.As(err, &se) {
		return se.StatusCode >= 500 || se.StatusCode == http.StatusForbidden || se.StatusCode == http.StatusTooManyRequests
	}
	return true
}

// putObject 按预签名地址上传，返回存储的 ETag。签名包含 Content-Length，由 req.ContentLength 发送
func (c *Client) putObject(ctx context.Context, p *presigned, body io.Reader, size int64) (string, error) {
	method := p.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL, body)
	if err != nil {
		return "", err
	}
	for k, v := range p.Headers {
		if !strings.EqualFold(k, "Content-Length") {
			req.Header.Set(k, v)
		}
	}
	req.ContentLength = size
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", &StorageError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Header.Get("ETag"), nil
}

// discard 上传失败后尽量删除待确认记录（释放配额，并由服务端清理存储上的残留）
func (c *Client) discard(ctx context.Context, id int64) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = c.Delete(dctx, id)
}
