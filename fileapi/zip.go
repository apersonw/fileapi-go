package fileapi

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// 压缩包预览：查看 ZIP 文件中的条目（服务端只读取文件末尾的目录，不下载整个文件），单独下载其中的文件。
// Windows 下打包的 GBK 文件名由服务端转为 UTF-8。

// ZipEntry ZIP 中的一个条目
type ZipEntry struct {
	// Index 条目序号（OpenZipEntry 使用）
	Index int `json:"index"`
	// Path 条目路径，目录以 / 结尾
	Path           string     `json:"path"`
	Dir            bool       `json:"dir,omitempty"`
	Size           int64      `json:"size"`
	CompressedSize int64      `json:"compressed_size"`
	ModifiedAt     *time.Time `json:"modified_at,omitempty"`
	Encrypted      bool       `json:"encrypted,omitempty"`
	// Downloadable 可以单独下载（未加密、存储或 Deflate 压缩的文件）
	Downloadable bool `json:"downloadable"`
}

// ZipListing ZIP 的条目列表；Truncated 时 Entries 只含前 5000 个，Total 为全部条目数
type ZipListing struct {
	Entries   []ZipEntry `json:"entries"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated"`
	// TotalSize 全部文件解压后的大小
	TotalSize int64  `json:"total_size"`
	Comment   string `json:"comment,omitempty"`
}

// ZipEntries 列出 ZIP 文件中的条目；不是 ZIP 或已损坏时返回 CodeBadRequest
func (c *Client) ZipEntries(ctx context.Context, id int64) (*ZipListing, error) {
	var l ZipListing
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/zip"), idempotent: true}, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// OpenZipEntry 经 FileApi 下载 ZIP 中第 index 个条目的解压内容（计入下载流量），用完关闭；
// 目录、加密条目或不支持的压缩方式返回 CodeBadRequest。服务端读完时校验 CRC-32，内容损坏时连接被中断（读取返回错误）
func (c *Client) OpenZipEntry(ctx context.Context, id int64, index int) (*Object, error) {
	return c.open(ctx, filePath(id, "/zip/"+strconv.Itoa(index)), "")
}
