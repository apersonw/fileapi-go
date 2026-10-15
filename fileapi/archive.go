package fileapi

import (
	"context"
	"net/http"
	"time"
)

// ArchiveFile 打包中的一个文件：Name 为包内路径（可含 / 表示目录），为空时使用原始文件名；同名时服务端自动加序号
type ArchiveFile struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// ArchiveOptions 打包选项（可为 nil）
type ArchiveOptions struct {
	// Filename 下载的 ZIP 文件名，默认 files-{时间}.zip
	Filename string
	// ExpiresIn 链接有效期，默认与上限同访问链接（服务端 access.default_ttl / access.max_ttl）
	ExpiresIn time.Duration
	// Compress 压缩（Deflate），默认不压缩直接打包
	Compress bool
	// Folder 同时打包整个目录（含下级目录，包内路径为相对该目录的路径）；只打包目录时 files 传 nil
	Folder string
}

// Archive 打包下载链接：有效期内可多次下载，每次下载时服务端重新检查文件（已删除的写入包内的 _errors.txt）
type Archive struct {
	URL       string    `json:"url"`
	Filename  string    `json:"filename"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateArchive 签发批量打包下载链接：访问链接时服务端边读边写成 ZIP 流式返回，按实际字节计入下载流量
func (c *Client) CreateArchive(ctx context.Context, files []ArchiveFile, opts *ArchiveOptions) (*Archive, error) {
	body := map[string]any{}
	if len(files) > 0 {
		body["files"] = files
	}
	if opts != nil {
		if opts.Folder != "" {
			body["folder"] = opts.Folder
		}
		if opts.Filename != "" {
			body["filename"] = opts.Filename
		}
		if n := expiresIn(opts.ExpiresIn); n > 0 {
			body["expires_in"] = n
		}
		if opts.Compress {
			body["compress"] = true
		}
	}
	var a Archive
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/archives", json: jsonBody(body)}, &a); err != nil {
		return nil, err
	}
	return &a, nil
}
