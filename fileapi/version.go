package fileapi

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// FileVersion 文件的历史版本
type FileVersion struct {
	// ID 历史版本 ID，只能用于版本相关的方法
	ID               int64  `json:"id"`
	Version          int    `json:"version"`
	OriginalFilename string `json:"original_filename"`
	ContentType      string `json:"content_type"`
	Size             int64  `json:"size"`
	FormatSize       string `json:"format_size"`
	SHA256           string `json:"sha256,omitempty"`
	HasThumbnail     bool   `json:"has_thumbnail"`
	// CreatedAt 该版本内容的上传时间
	CreatedAt time.Time `json:"created_at"`
}

func versionPath(id, versionID int64, suffix string) string {
	return filePath(id, "/versions/"+strconv.FormatInt(versionID, 10)+suffix)
}

// ListVersions 文件的历史版本（版本号大的在前）。上传新版本见 UploadOptions.VersionOf / DirectUploadOptions.VersionOf
func (c *Client) ListVersions(ctx context.Context, id int64) ([]FileVersion, error) {
	var res struct {
		List []FileVersion `json:"list"`
	}
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/versions"), idempotent: true}, &res); err != nil {
		return nil, err
	}
	return res.List, nil
}

// OpenVersion 经 FileApi 代理下载历史版本的内容
func (c *Client) OpenVersion(ctx context.Context, id, versionID int64) (*Object, error) {
	return c.open(ctx, versionPath(id, versionID, "/download"), "")
}

// RestoreVersion 把历史版本恢复为文件的当前内容：当前内容转为历史版本（不丢失），版本号加 1，文件 ID 与链接不变
func (c *Client) RestoreVersion(ctx context.Context, id, versionID int64) (*File, error) {
	var f File
	if err := c.call(ctx, &request{method: http.MethodPost, path: versionPath(id, versionID, "/restore")}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// DeleteVersion 删除一个历史版本
func (c *Client) DeleteVersion(ctx context.Context, id, versionID int64) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: versionPath(id, versionID, ""), idempotent: true}, nil)
}
