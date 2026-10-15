package fileapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"
)

// InstantOptions 秒传选项
type InstantOptions struct {
	// ObjectType / ObjectID 关联的业务对象。同一对象已有文件且未设置 ReplaceExisting 时直接返回已有文件（Reused=true）
	ObjectType      string
	ObjectID        string
	ReplaceExisting bool
	Metadata        map[string]string
	// ExpiresIn 多久后过期（到期后服务端自动删除，按秒向上取整），0 表示不过期
	ExpiresIn time.Duration
	// Folder 所在目录，默认根目录
	Folder string
	// TagIDs 文件的标签（本客户端的标签，最多 20 个）
	TagIDs []int64
	// Transcode 上传完成后转码为多清晰度 HLS（见 Play）；Profiles 档位（如 "720p"），指定即视为转码，为空用服务端默认
	Transcode bool
	Profiles  []string
}

// InstantResult 秒传结果。Hit 为 false 表示服务端没有相同内容的文件，需正常上传
type InstantResult struct {
	Hit bool `json:"hit"`
	// Reused 命中同一业务对象的已有文件
	Reused bool  `json:"reused"`
	File   *File `json:"file,omitempty"`
}

// UploadInstant 秒传：按内容的 SHA-256（十六进制）与大小复用本客户端已有的相同文件，不传输文件内容。
// 只会命中本客户端经代理上传或秒传的文件；服务端未开启去重时总是未命中。
func (c *Client) UploadInstant(ctx context.Context, filename, sha256Hex string, size int64, opts *InstantOptions) (*InstantResult, error) {
	if filename == "" {
		return nil, errors.New("fileapi: filename is required")
	}
	if len(sha256Hex) != sha256.Size*2 || size <= 0 {
		return nil, errors.New("fileapi: instant upload requires a SHA-256 hex digest and a positive size")
	}
	if opts == nil {
		opts = &InstantOptions{}
	}
	body := map[string]any{
		"filename": filename, "size": size, "sha256": sha256Hex, "object_type": opts.ObjectType,
		"object_id": opts.ObjectID, "replace_existing": opts.ReplaceExisting, "metadata": opts.Metadata,
	}
	if n := expiresIn(opts.ExpiresIn); n > 0 {
		body["expires_in"] = n
	}
	if opts.Folder != "" {
		body["folder"] = opts.Folder
	}
	if len(opts.TagIDs) > 0 {
		body["tag_ids"] = opts.TagIDs
	}
	if opts.Transcode || len(opts.Profiles) > 0 {
		body["transcode"], body["profiles"] = true, opts.Profiles
	}
	var res InstantResult
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/instant", json: jsonBody(body)}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// HashSHA256 计算内容的 SHA-256（十六进制）与长度，用于秒传
func HashSHA256(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// tryInstant 先读一遍内容（content，长度应为 size）计算摘要再尝试秒传；命中时进度直接到 100%
func (c *Client) tryInstant(ctx context.Context, filename string, content io.Reader, size int64, opts *InstantOptions,
	progress func(sent, total int64)) (*InstantResult, error) {
	sum, n, err := HashSHA256(content)
	if err != nil {
		return nil, err
	}
	if n != size {
		return nil, io.ErrUnexpectedEOF
	}
	res, err := c.UploadInstant(ctx, filename, sum, size, opts)
	if err != nil {
		return nil, err
	}
	if res.Hit && progress != nil {
		progress(size, size)
	}
	return res, nil
}
