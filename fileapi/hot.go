package fileapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// HotFile 热门文件：统计期内的访问次数与字节
type HotFile struct {
	File   File  `json:"file"`
	Visits int64 `json:"visits"`
	Bytes  int64 `json:"bytes"`
}

// HotFiles 最近 days 天（含今天，默认 7，最长 365）经统一访问入口与代理下载访问次数最多的 limit 个文件（默认 10，最多 100）；
// 整个文件或从头开始的分段请求计一次，Range 续传、HEAD 与缩略图不计
func (c *Client) HotFiles(ctx context.Context, days, limit int) ([]HotFile, error) {
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var res struct {
		List []HotFile `json:"list"`
	}
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/hot-files", query: q, idempotent: true}, &res); err != nil {
		return nil, err
	}
	return res.List, nil
}
