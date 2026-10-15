package fileapi

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// TagRule 自动打标签规则：上传时满足全部条件的新文件自动打上 TagIDs（与上传时指定的标签合并，每个文件最多 20 个）；
// 条件为零值时不限，至少设置一个
type TagRule struct {
	ID       int64   `json:"id"`
	ClientID string  `json:"client_id"`
	Name     string  `json:"name"`
	TagIDs   []int64 `json:"tag_ids"`
	// Category 分类（Category* 常量）；Extensions 扩展名之一（如 "pdf"，服务端规范化为小写带点）
	Category   string   `json:"category,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	// Folder 所在目录，Recursive 含下级目录
	Folder     string `json:"folder,omitempty"`
	Recursive  bool   `json:"recursive,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	// Metadata 元数据须包含的键值
	Metadata map[string]string `json:"metadata,omitempty"`
	// MinSize / MaxSize 大小范围（字节，含边界），0 不限
	MinSize   int64     `json:"min_size,omitempty"`
	MaxSize   int64     `json:"max_size,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TagRulePreview 试运行结果：Files 可以补打的已有文件数，OverLimit 补打后会超过每个文件标签上限而跳过的文件数
type TagRulePreview struct {
	Files     int64 `json:"files"`
	OverLimit int64 `json:"over_limit"`
}

// TagRuleApply 补打结果：Updated 本次补打的文件数，Remaining 为 true 时还有文件未处理（再次调用继续）
type TagRuleApply struct {
	Updated   int64 `json:"updated"`
	Remaining bool  `json:"remaining"`
}

func tagRulePath(id int64, suffix string) string {
	return "/open/v1/tag-rules/" + strconv.FormatInt(id, 10) + suffix
}

// ruleBody 请求体只取规则的名称、标签、条件与启用状态
func ruleBody(r TagRule) map[string]any {
	body := map[string]any{"name": r.Name, "tag_ids": r.TagIDs, "enabled": r.Enabled}
	if r.Category != "" {
		body["category"] = r.Category
	}
	if len(r.Extensions) > 0 {
		body["extensions"] = r.Extensions
	}
	if r.Folder != "" {
		body["folder"], body["recursive"] = r.Folder, r.Recursive
	}
	if r.ObjectType != "" {
		body["object_type"] = r.ObjectType
	}
	if len(r.Metadata) > 0 {
		body["metadata"] = r.Metadata
	}
	if r.MinSize > 0 {
		body["min_size"] = r.MinSize
	}
	if r.MaxSize > 0 {
		body["max_size"] = r.MaxSize
	}
	return body
}

// ListTagRules 本客户端的自动打标签规则
func (c *Client) ListTagRules(ctx context.Context) ([]TagRule, error) {
	var out struct {
		List []TagRule `json:"list"`
	}
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/tag-rules", idempotent: true}, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// CreateTagRule 新建自动打标签规则（rule.Enabled 为 false 时规则先不生效）；每个客户端最多 50 条
func (c *Client) CreateTagRule(ctx context.Context, rule TagRule) (*TagRule, error) {
	var out TagRule
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/tag-rules", json: jsonBody(ruleBody(rule))}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateTagRule 修改规则（替换名称、标签、条件与启用状态，只影响之后上传的文件）
func (c *Client) UpdateTagRule(ctx context.Context, id int64, rule TagRule) (*TagRule, error) {
	var out TagRule
	if err := c.call(ctx, &request{method: http.MethodPut, path: tagRulePath(id, ""), json: jsonBody(ruleBody(rule)), idempotent: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTagRule 删除规则（已打上的标签不变）
func (c *Client) DeleteTagRule(ctx context.Context, id int64) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: tagRulePath(id, ""), idempotent: true}, nil)
}

// PreviewTagRule 试运行：按 rule 的条件统计已有文件中可以补打的文件数（无需保存规则）
func (c *Client) PreviewTagRule(ctx context.Context, rule TagRule) (*TagRulePreview, error) {
	var out TagRulePreview
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/tag-rules/preview", json: jsonBody(ruleBody(rule)), idempotent: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyTagRule 按已保存的规则给已有文件补打标签，每次最多处理 5 万个；Remaining 为 true 时再次调用继续
func (c *Client) ApplyTagRule(ctx context.Context, id int64) (*TagRuleApply, error) {
	var out TagRuleApply
	if err := c.call(ctx, &request{method: http.MethodPost, path: tagRulePath(id, "/apply")}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
