package fileapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// 收到的文件的元数据键：收集码、提交者填写的姓名与备注
const (
	CollectionMetaKey   = "collection"
	UploaderMetaKey     = "uploader"
	UploaderNoteMetaKey = "uploader_note"
)

// Collection 文件收集：URL 为 FileApi 自带的收集页（{base}/c/{code}），外部人员无需登录即可上传文件到 Folder
type Collection struct {
	Code        string   `json:"code"`
	URL         string   `json:"url"`
	ClientID    string   `json:"client_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Folder      string   `json:"folder"`
	TagIDs      []int64  `json:"tag_ids"`
	Extensions  []string `json:"extensions"`
	// MaxFileSize 单文件上限（0 为客户端与代理上传的上限），MaxFiles 最多收到的文件数（0 不限）
	MaxFileSize       int64 `json:"max_file_size"`
	MaxFiles          int   `json:"max_files"`
	RequireName       bool  `json:"require_name"`
	GroupByUploader   bool  `json:"group_by_uploader"`
	PasswordProtected bool  `json:"password_protected"`
	Enabled           bool  `json:"enabled"`
	// Files 已收到的文件数（删除文件不归还额度），Bytes 收到的文件合计大小
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Submissions 提交次数（一次提交可包含多个文件，至少成功一个才计入）
	Submissions     int        `json:"submissions"`
	LastSubmittedAt *time.Time `json:"last_submitted_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CollectionOptions 新建文件收集的选项（可为 nil）
type CollectionOptions struct {
	// Description 收集页上显示的说明
	Description string
	// Folder 收到的文件所在目录，空时为 /文件收集/{标题}/
	Folder string
	// TagIDs 收到的文件自动打上的标签
	TagIDs []int64
	// Extensions 允许的扩展名（如 "pdf"，与客户端的白名单取交集）
	Extensions []string
	// MaxFileSize 单文件大小上限（字节），MaxFiles 最多收到的文件数，0 不限
	MaxFileSize int64
	MaxFiles    int
	// RequireName 提交时须填写姓名；GroupByUploader 按姓名分子目录（隐含须填写姓名）
	RequireName     bool
	GroupByUploader bool
	// Password 提取码（4 ~ 16 位字母或数字）
	Password string
	// ExpiresIn 多久后截止（按秒向上取整），0 表示不截止
	ExpiresIn time.Duration
}

// CollectionUpdate 修改文件收集，nil 的字段不变
type CollectionUpdate struct {
	Title       *string
	Description *string
	// Enabled false 关闭收集（收集页不再接受提交），true 重新开放
	Enabled  *bool
	MaxFiles *int
	// ExpiresIn 重新设置截止时间（多久后），0 表示不截止
	ExpiresIn *time.Duration
}

// CollectionPage 文件收集列表的一页
type CollectionPage struct {
	List     []Collection `json:"list"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func collectionPath(code string) string { return "/open/v1/collections/" + url.PathEscape(code) }

// CreateCollection 新建文件收集，返回收集页地址
func (c *Client) CreateCollection(ctx context.Context, title string, opts *CollectionOptions) (*Collection, error) {
	if title == "" {
		return nil, errors.New("fileapi: collection title is required")
	}
	body := map[string]any{"title": title}
	if opts != nil {
		if opts.Description != "" {
			body["description"] = opts.Description
		}
		if opts.Folder != "" {
			body["folder"] = opts.Folder
		}
		if len(opts.TagIDs) > 0 {
			body["tag_ids"] = opts.TagIDs
		}
		if len(opts.Extensions) > 0 {
			body["extensions"] = opts.Extensions
		}
		if opts.MaxFileSize > 0 {
			body["max_file_size"] = opts.MaxFileSize
		}
		if opts.MaxFiles > 0 {
			body["max_files"] = opts.MaxFiles
		}
		if opts.RequireName {
			body["require_name"] = true
		}
		if opts.GroupByUploader {
			body["group_by_uploader"] = true
		}
		if opts.Password != "" {
			body["password"] = opts.Password
		}
		if n := expiresIn(opts.ExpiresIn); n > 0 {
			body["expires_in"] = n
		}
	}
	var out Collection
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/collections", json: jsonBody(body)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Collections 本客户端的文件收集（新的在前）；page 从 1 开始，pageSize 为 0 时用服务端默认
func (c *Client) Collections(ctx context.Context, page, pageSize int) (*CollectionPage, error) {
	q := url.Values{}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	var p CollectionPage
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/collections", query: q, idempotent: true}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Collection 查询文件收集（含已收到的文件数与大小）
func (c *Client) Collection(ctx context.Context, code string) (*Collection, error) {
	var out Collection
	if err := c.call(ctx, &request{method: http.MethodGet, path: collectionPath(code), idempotent: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCollection 修改文件收集：标题、说明、开放 / 关闭、份数上限、截止时间
func (c *Client) UpdateCollection(ctx context.Context, code string, u CollectionUpdate) (*Collection, error) {
	body := map[string]any{}
	if u.Title != nil {
		body["title"] = *u.Title
	}
	if u.Description != nil {
		body["description"] = *u.Description
	}
	if u.Enabled != nil {
		body["enabled"] = *u.Enabled
	}
	if u.MaxFiles != nil {
		body["max_files"] = *u.MaxFiles
	}
	if u.ExpiresIn != nil {
		body["expires_in"] = expiresIn(*u.ExpiresIn)
	}
	var out Collection
	if err := c.call(ctx, &request{method: http.MethodPatch, path: collectionPath(code), json: jsonBody(body), idempotent: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCollection 删除文件收集（收集页立即失效，收到的文件保留）
func (c *Client) DeleteCollection(ctx context.Context, code string) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: collectionPath(code), idempotent: true}, nil)
}

// CollectionFiles 某个收集收到的文件（按元数据 collection 筛选）；q 的其它条件照常生效，可为 nil
func (c *Client) CollectionFiles(ctx context.Context, code string, q *ListQuery) (*FilePage, error) {
	query := ListQuery{}
	if q != nil {
		query = *q
	}
	meta := map[string]string{CollectionMetaKey: code}
	for k, v := range query.Metadata {
		if k != CollectionMetaKey {
			meta[k] = v
		}
	}
	query.Metadata = meta
	return c.List(ctx, &query)
}
