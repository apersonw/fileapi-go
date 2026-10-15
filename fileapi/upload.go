package fileapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UploadOptions 代理上传选项
type UploadOptions struct {
	// ObjectType / ObjectID 关联的业务对象。同一对象已有文件且未设置 ReplaceExisting 时直接返回已有文件（Reused=true）
	ObjectType      string
	ObjectID        string
	ReplaceExisting bool
	// Metadata 自定义元数据（字符串键值对）
	Metadata map[string]string
	// Progress 进度回调：已发送的文件字节数与总字节数（未知为 -1）
	Progress func(sent, total int64)
	// ExpiresIn 多久后过期（到期后服务端自动删除，按秒向上取整），0 表示不过期
	ExpiresIn time.Duration
	// TryInstant 先读一遍内容计算 SHA-256 尝试秒传（见 UploadInstant），命中时不再上传（UploadResult.Instant=true）。
	// 需要 r 实现 io.Seeker 且 size 已知（UploadFile 满足）；上传新版本（VersionOf）时不尝试
	TryInstant bool
	// VersionOf 作为该文件的新版本上传：文件 ID、业务对象、元数据、过期时间与已签发的链接不变，旧内容保留为历史版本，
	// 返回文件的最新信息（File.Version 加 1）。不能与 ObjectType、ReplaceExisting、Metadata、ExpiresIn 同时使用
	VersionOf int64
	// Folder 所在目录（如 "/合同/2026"），默认根目录；不能与 VersionOf 同时使用（新版本沿用文件的目录）
	Folder string
	// TagIDs 文件的标签（本客户端的标签，最多 20 个）；不能与 VersionOf 同时使用（标签属于文件，新版本不改变）
	TagIDs []int64
	// Transcode 上传完成后转码为多清晰度 HLS（见 Play）；Profiles 档位（如 "720p"），指定即视为转码，为空用服务端默认
	Transcode bool
	Profiles  []string
}

// Upload 代理上传：文件经 FileApi 写入存储。内容以流的方式发送，不整体读入内存；
// SDK 边发送边计算 SHA-256 并附在表单末尾，服务端校验不一致时拒绝并丢弃该文件。
//
// size 为内容长度（读取 size 字节），未知时传 -1（以分块编码发送）。
// 代理上传受服务端 max_proxy_file_size 限制，大文件请用 UploadDirect。
func (c *Client) Upload(ctx context.Context, filename string, r io.Reader, size int64, opts *UploadOptions) (*UploadResult, error) {
	if filename == "" {
		return nil, errors.New("fileapi: filename is required")
	}
	if opts == nil {
		opts = &UploadOptions{}
	}
	if opts.TryInstant && opts.VersionOf == 0 {
		res, err := c.instantBeforeUpload(ctx, filename, r, size, opts)
		if err != nil || res != nil {
			return res, err
		}
	}
	body, contentType, length, err := uploadBody(filename, r, size, opts)
	if err != nil {
		return nil, err
	}
	var res UploadResult
	req := &request{method: http.MethodPost, path: "/open/v1/files", stream: body, contentType: contentType, contentLength: length}
	if err := c.call(ctx, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UploadFile 代理上传本地文件，文件名取自路径
func (c *Client) UploadFile(ctx context.Context, path string, opts *UploadOptions) (*UploadResult, error) {
	f, st, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return c.Upload(ctx, filepath.Base(path), f, st.Size(), opts)
}

// instantBeforeUpload 代理上传前尝试秒传：计算摘要后把读取位置还原。未命中时返回 (nil, nil)
func (c *Client) instantBeforeUpload(ctx context.Context, filename string, r io.Reader, size int64, opts *UploadOptions) (*UploadResult, error) {
	rs, ok := r.(io.ReadSeeker)
	if !ok || size < 0 {
		return nil, errors.New("fileapi: TryInstant requires an io.ReadSeeker with a known size")
	}
	pos, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	res, err := c.tryInstant(ctx, filename, io.LimitReader(rs, size), size, &InstantOptions{
		ObjectType: opts.ObjectType, ObjectID: opts.ObjectID, ReplaceExisting: opts.ReplaceExisting, Metadata: opts.Metadata,
		ExpiresIn: opts.ExpiresIn, Folder: opts.Folder, TagIDs: opts.TagIDs, Transcode: opts.Transcode, Profiles: opts.Profiles,
	}, opts.Progress)
	if err != nil {
		return nil, err
	}
	if res.Hit {
		return &UploadResult{File: *res.File, Reused: res.Reused, Instant: true}, nil
	}
	if _, err := rs.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	return nil, nil
}

func openRegular(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("fileapi: %s is not a regular file", path)
	}
	return f, st, nil
}

// uploadBody 构造流式 multipart 表单：普通字段 + 文件 + 文件之后的 sha256 字段。
// size 已知时总长度可以预先算出（sha256 固定 64 个字符），据此设置 Content-Length。
func uploadBody(filename string, r io.Reader, size int64, opts *UploadOptions) (io.Reader, string, int64, error) {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	fields := [][2]string{{"object_type", opts.ObjectType}, {"object_id", opts.ObjectID}}
	if len(opts.TagIDs) > 0 {
		fields = append(fields, [2]string{"tag_ids", joinIDs(opts.TagIDs)})
	}
	if opts.Folder != "" {
		fields = append(fields, [2]string{"folder", opts.Folder})
	}
	if opts.Transcode {
		fields = append(fields, [2]string{"transcode", "true"})
	}
	if len(opts.Profiles) > 0 {
		fields = append(fields, [2]string{"profiles", strings.Join(opts.Profiles, ",")})
	}
	if opts.ReplaceExisting {
		fields = append(fields, [2]string{"replace_existing", "true"})
	}
	if opts.VersionOf > 0 {
		fields = append(fields, [2]string{"version_of", strconv.FormatInt(opts.VersionOf, 10)})
	}
	if n := expiresIn(opts.ExpiresIn); n > 0 {
		fields = append(fields, [2]string{"expires_in", strconv.FormatInt(n, 10)})
	}
	if len(opts.Metadata) > 0 {
		b, err := json.Marshal(opts.Metadata)
		if err != nil {
			return nil, "", 0, err
		}
		fields = append(fields, [2]string{"metadata", string(b)})
	}
	for _, f := range fields {
		if f[1] != "" {
			if err := mw.WriteField(f[0], f[1]); err != nil {
				return nil, "", 0, err
			}
		}
	}
	if _, err := mw.CreateFormFile("file", filename); err != nil {
		return nil, "", 0, err
	}

	h := sha256.New()
	src := r
	if size >= 0 {
		src = io.LimitReader(r, size)
	}
	src = io.TeeReader(src, h)
	if opts.Progress != nil {
		src = &progressReader{r: src, fn: func(n int64) { opts.Progress(n, size) }}
	}
	tail := &lazyReader{make: func() []byte { return multipartTail(mw.Boundary(), hex.EncodeToString(h.Sum(nil))) }}

	length := int64(-1)
	if size >= 0 {
		length = int64(head.Len()) + size + int64(len(multipartTail(mw.Boundary(), hashPlaceholder)))
	}
	return io.MultiReader(&head, src, tail), mw.FormDataContentType(), length, nil
}

var hashPlaceholder = strings.Repeat("0", sha256.Size*2)

// multipartTail 文件之后的 sha256 字段与结束边界，格式与 multipart.Writer 一致
func multipartTail(boundary, sum string) []byte {
	return []byte("\r\n--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="sha256"` + "\r\n\r\n" +
		sum + "\r\n--" + boundary + "--\r\n")
}

// lazyReader 首次读取时才生成内容（文件读完后才能得到摘要）
type lazyReader struct {
	make func() []byte
	r    *bytes.Reader
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil {
		l.r = bytes.NewReader(l.make())
	}
	return l.r.Read(p)
}

// progressReader 累计读取字节数并回调
type progressReader struct {
	r  io.Reader
	n  int64
	fn func(n int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		p.fn(p.n)
	}
	return n, err
}

// progress 并发上传时汇总各分片的进度，回调串行执行
type progress struct {
	mu    sync.Mutex
	sent  int64
	total int64
	fn    func(sent, total int64)
}

func (p *progress) add(n int64) {
	if p == nil || p.fn == nil || n == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent += n
	p.fn(p.sent, p.total)
}

// countingReader 把读取的字节数计入 progress，失败重试时可整体回退
type countingReader struct {
	r io.Reader
	p *progress
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if n > 0 {
		c.n += int64(n)
		c.p.add(int64(n))
	}
	return n, err
}

func (c *countingReader) rollback() {
	c.p.add(-c.n)
	c.n = 0
}
