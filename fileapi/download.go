package fileapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Object 文件内容流，调用方负责 Close
type Object struct {
	io.ReadCloser
	// ContentType 文件类型
	ContentType string
	// ContentLength 本次返回的字节数，未知为 -1
	ContentLength int64
	// Size 文件总大小（区间下载时取自 Content-Range），未知为 -1
	Size int64
	// Filename 原始文件名
	Filename string
}

// Open 经 FileApi 代理下载文件内容
func (c *Client) Open(ctx context.Context, id int64) (*Object, error) {
	return c.open(ctx, filePath(id, "/download"), "")
}

// OpenRange 下载文件的一段：从 offset 开始的 length 字节，length < 0 表示读到末尾
func (c *Client) OpenRange(ctx context.Context, id int64, offset, length int64) (*Object, error) {
	if offset < 0 || length == 0 {
		return nil, errors.New("fileapi: invalid range")
	}
	rng := fmt.Sprintf("bytes=%d-", offset)
	if length > 0 {
		rng += strconv.FormatInt(offset+length-1, 10)
	}
	return c.open(ctx, filePath(id, "/download"), rng)
}

func (c *Client) open(ctx context.Context, path, rng string) (*Object, error) {
	r := &request{method: http.MethodGet, path: path, idempotent: true}
	if rng != "" {
		r.header = http.Header{"Range": {rng}}
	}
	resp, err := c.send(ctx, r)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusPartialContent && rng != "":
	default:
		if resp.StatusCode/100 == 2 {
			resp.Body.Close()
			return nil, fmt.Errorf("fileapi: unexpected download status %d", resp.StatusCode)
		}
		return nil, decodeError(resp)
	}
	obj := &Object{ReadCloser: resp.Body, ContentType: resp.Header.Get("Content-Type"), ContentLength: resp.ContentLength, Size: resp.ContentLength}
	if resp.StatusCode == http.StatusPartialContent {
		obj.Size = totalFromContentRange(resp.Header.Get("Content-Range"))
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		obj.Filename = params["filename"]
	}
	return obj, nil
}

// totalFromContentRange 解析 "bytes 0-99/1234" 中的总大小
func totalFromContentRange(v string) int64 {
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		if n, err := strconv.ParseInt(v[i+1:], 10, 64); err == nil {
			return n
		}
	}
	return -1
}

// Download 下载文件内容写入 w，返回写入的字节数；内容不完整时返回 io.ErrUnexpectedEOF
func (c *Client) Download(ctx context.Context, id int64, w io.Writer) (int64, error) {
	obj, err := c.Open(ctx, id)
	if err != nil {
		return 0, err
	}
	defer obj.Close()
	n, err := io.Copy(w, obj)
	if err != nil {
		return n, err
	}
	if obj.ContentLength >= 0 && n != obj.ContentLength {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

// DownloadFile 下载到本地文件：先写同目录下的临时文件，完整写入后再改名，失败不会留下半截文件
func (c *Client) DownloadFile(ctx context.Context, id int64, path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := c.Download(ctx, id, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
