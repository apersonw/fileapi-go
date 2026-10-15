package fileapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// 相似图片：服务端后台为图片计算感知哈希（dHash），两张图哈希的汉明距离（Distance，0 ~ 64）越小越相似，
// 0 表示几乎相同，一般 ≤ 10 可认为相似；缩放、重新压缩、轻微调色后仍能识别。新上传的图片稍后才能参与比较。

// SimilarOptions 查找相似图片的选项
type SimilarOptions struct {
	// Threshold 最大汉明距离（0 ~ 20），nil 使用默认值 8
	Threshold *int
	// Limit 条数（默认 20，最多 100）；分组时为组数（默认 50，最多 200）
	Limit int
	// Folder 只看该目录中的图片（仅分组）
	Folder string
}

// SimilarFile 相似的图片
type SimilarFile struct {
	File     File `json:"file"`
	Distance int  `json:"distance"`
}

// SimilarResult 与某张图片相似的图片；Hashed 为 false 表示该图片的哈希还没计算（稍后再查）
type SimilarResult struct {
	Hashed bool          `json:"hashed"`
	List   []SimilarFile `json:"list"`
}

// SimilarGroup 一组相似图片：第一张为组内最新的图片，Distance 为与它的距离
type SimilarGroup struct {
	Files []SimilarFile `json:"files"`
}

// SimilarGroups 相似图片分组；Total 为组数（返回的可能更少），Scanned 比较的图片数，Truncated 表示图片超过 10000 张只比较了最新的
type SimilarGroups struct {
	Groups    []SimilarGroup `json:"groups"`
	Total     int            `json:"total"`
	Scanned   int            `json:"scanned"`
	Truncated bool           `json:"truncated"`
}

func (o *SimilarOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Threshold != nil {
		q.Set("threshold", strconv.Itoa(*o.Threshold))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Folder != "" {
		q.Set("folder", o.Folder)
	}
	return q
}

// SimilarImages 与该图片相似的图片（按距离从近到远）；不是图片时返回 CodeBadRequest
func (c *Client) SimilarImages(ctx context.Context, id int64, opts *SimilarOptions) (*SimilarResult, error) {
	var res SimilarResult
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/similar"), query: opts.values(), idempotent: true}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SimilarImageGroups 把最近的（最多 10000 张）图片按相似度分组，只返回两张及以上的组（查找重复图片）
func (c *Client) SimilarImageGroups(ctx context.Context, opts *SimilarOptions) (*SimilarGroups, error) {
	var res SimilarGroups
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/files/similar-groups", query: opts.values(), idempotent: true}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
