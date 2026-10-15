package fileapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Share 分享链接：URL 为 FileApi 自带的分享页（{base}/s/{code}），访问者可在线预览、下载与打包下载
type Share struct {
	Code     string  `json:"code"`
	URL      string  `json:"url"`
	ClientID string  `json:"client_id"`
	Title    string  `json:"title"`
	FileIDs  []int64 `json:"file_ids"`
	// Folder 分享的目录（目录分享时，访问时实时读取目录中的文件），Recursive 含下级目录
	Folder            string `json:"folder,omitempty"`
	Recursive         bool   `json:"recursive"`
	PasswordProtected bool   `json:"password_protected"`
	AllowDownload     bool   `json:"allow_download"`
	// MaxDownloads 下载次数上限（0 不限），Downloads 已下载次数（单个文件下载与打包下载各计一次），Views 分享页浏览次数
	MaxDownloads int        `json:"max_downloads"`
	Downloads    int        `json:"downloads"`
	Views        int        `json:"views"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ShareOptions 新建分享的选项（可为 nil）
type ShareOptions struct {
	// Title 分享页标题，空时取第一个文件名或目录名
	Title string
	// Password 提取码（4 ~ 16 位字母或数字）
	Password string
	// ExpiresIn 有效期（按秒向上取整），0 表示永久有效
	ExpiresIn time.Duration
	// MaxDownloads 下载次数上限，0 不限
	MaxDownloads int
	// PreviewOnly 只能在线预览，不能下载
	PreviewOnly bool
}

// SharePage 分享列表的一页
type SharePage struct {
	List     []Share `json:"list"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

func shareBody(opts *ShareOptions) map[string]any {
	body := map[string]any{}
	if opts != nil {
		if opts.Title != "" {
			body["title"] = opts.Title
		}
		if opts.Password != "" {
			body["password"] = opts.Password
		}
		if n := expiresIn(opts.ExpiresIn); n > 0 {
			body["expires_in"] = n
		}
		if opts.MaxDownloads > 0 {
			body["max_downloads"] = opts.MaxDownloads
		}
		if opts.PreviewOnly {
			body["allow_download"] = false
		}
	}
	return body
}

// ShareFiles 分享若干文件（最多 1000 个），返回分享页地址
func (c *Client) ShareFiles(ctx context.Context, ids []int64, opts *ShareOptions) (*Share, error) {
	body := shareBody(opts)
	body["file_ids"] = ids
	return c.createShare(ctx, body)
}

// ShareTags 分享包含全部这些标签的文件：创建时确定文件（最多 1000 个，之后打标签的文件不会加入）
func (c *Client) ShareTags(ctx context.Context, tagIDs []int64, opts *ShareOptions) (*Share, error) {
	body := shareBody(opts)
	body["tag_ids"] = tagIDs
	return c.createShare(ctx, body)
}

// ShareFolder 分享整个目录（访问时实时读取目录中的文件），recursive 含下级目录
func (c *Client) ShareFolder(ctx context.Context, folder string, recursive bool, opts *ShareOptions) (*Share, error) {
	body := shareBody(opts)
	body["folder"], body["recursive"] = folder, recursive
	return c.createShare(ctx, body)
}

func (c *Client) createShare(ctx context.Context, body map[string]any) (*Share, error) {
	var s Share
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/shares", json: jsonBody(body)}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Shares 本客户端的分享（新的在前）；page 从 1 开始，pageSize 为 0 时用服务端默认
func (c *Client) Shares(ctx context.Context, page, pageSize int) (*SharePage, error) {
	q := url.Values{}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	var p SharePage
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/shares", query: q, idempotent: true}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Share 查询分享（含下载与浏览次数）
func (c *Client) Share(ctx context.Context, code string) (*Share, error) {
	var s Share
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/shares/" + url.PathEscape(code), idempotent: true}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteShare 取消分享（立即失效）
func (c *Client) DeleteShare(ctx context.Context, code string) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: "/open/v1/shares/" + url.PathEscape(code), idempotent: true}, nil)
}
