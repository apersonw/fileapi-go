package fileapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 文件标签：每个客户端有自己的标签库（名称不区分大小写唯一，最多 200 个，可设颜色），每个文件最多 20 个标签；
// ListQuery.TagIDs 按标签筛选。标签属于文件而非内容，上传新版本不改变。

// 预设的标签颜色（也可以用 #RRGGBB）
const (
	TagGray   = "gray"
	TagRed    = "red"
	TagOrange = "orange"
	TagYellow = "yellow"
	TagGreen  = "green"
	TagTeal   = "teal"
	TagBlue   = "blue"
	TagPurple = "purple"
	TagPink   = "pink"
)

// TagBrief 文件上的标签
type TagBrief struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Tag 标签库中的标签；Files 带该标签的正常文件数（ListTags 返回）
type Tag struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color,omitempty"`
	Files     int64     `json:"files"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func tagPath(id int64) string { return "/open/v1/tags/" + strconv.FormatInt(id, 10) }

// ListTags 本客户端的标签库（按名称排序）
func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	var res struct {
		List []Tag `json:"list"`
	}
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/tags", idempotent: true}, &res); err != nil {
		return nil, err
	}
	return res.List, nil
}

// CreateTag 新建标签（名称 1 ~ 32 个字符；同名返回 CodeConflict）；color 为预设色（TagBlue 等）、#RRGGBB 或空
func (c *Client) CreateTag(ctx context.Context, name, color string) (*Tag, error) {
	var t Tag
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/tags", json: jsonBody(map[string]string{"name": name, "color": color})}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTag 修改标签的名称与颜色（文件上的标签随之改变）
func (c *Client) UpdateTag(ctx context.Context, id int64, name, color string) (*Tag, error) {
	var t Tag
	req := &request{method: http.MethodPatch, path: tagPath(id), json: jsonBody(map[string]string{"name": name, "color": color}), idempotent: true}
	if err := c.call(ctx, req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTag 删除标签并从全部文件上移除，返回移除了标签的文件数
func (c *Client) DeleteTag(ctx context.Context, id int64) (int64, error) {
	var res struct {
		UntaggedFiles int64 `json:"untagged_files"`
	}
	if err := c.call(ctx, &request{method: http.MethodDelete, path: tagPath(id), idempotent: true}, &res); err != nil {
		return 0, err
	}
	return res.UntaggedFiles, nil
}

// SetFileTags 把文件的标签替换为 tagIDs（nil 或空清除），返回文件的最新信息
func (c *Client) SetFileTags(ctx context.Context, id int64, tagIDs []int64) (*File, error) {
	if tagIDs == nil {
		tagIDs = []int64{}
	}
	var f File
	req := &request{method: http.MethodPut, path: filePath(id, "/tags"), json: jsonBody(map[string]any{"tag_ids": tagIDs}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// BatchTag 给最多 100 个文件添加、移除标签，返回修改的文件数；有文件的标签超过 20 个时整批不修改（CodeBadRequest）
func (c *Client) BatchTag(ctx context.Context, ids, add, remove []int64) (int64, error) {
	var res struct {
		Updated int64 `json:"updated"`
	}
	body := map[string]any{"ids": ids, "add": add, "remove": remove}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/batch-tag", json: jsonBody(body), idempotent: true}, &res); err != nil {
		return 0, err
	}
	return res.Updated, nil
}

// joinIDs 逗号分隔的 ID（代理上传表单的 tag_ids）
func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
