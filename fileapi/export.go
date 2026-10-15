package fileapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// ExportResult 清单导出的结果：Rows 导出的行数，Truncated 超过服务端上限（100 万行）被截断
type ExportResult struct {
	Rows      int64
	Truncated bool
}

// ExportFiles 按与 List 相同的筛选与排序条件（忽略分页与游标）导出本客户端的文件清单 CSV，流式写入 w（UTF-8 带 BOM）。
// 列：id、original_filename、folder、ext、content_type、category、size、sha256、object_type、object_id、tags（分号分隔）、
// metadata（JSON）、width、height、duration_ms、version、expires_at、retain_until、legal_hold、created_at、updated_at。
// 行数与是否截断取自响应 trailer；导出中途服务端出错时连接被中断，返回错误
func (c *Client) ExportFiles(ctx context.Context, q *ListQuery, w io.Writer) (*ExportResult, error) {
	v, err := q.values()
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"page", "page_size", "cursor", "with_total"} {
		v.Del(k)
	}
	resp, err := c.send(ctx, &request{method: http.MethodGet, path: "/open/v1/files/export", query: v, idempotent: true})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(w, resp.Body); err != nil {
		return nil, fmt.Errorf("fileapi: export interrupted: %w", err)
	}
	res := &ExportResult{Truncated: resp.Trailer.Get("X-Export-Truncated") == "true"}
	rows := resp.Trailer.Get("X-Export-Rows")
	if rows == "" {
		return nil, fmt.Errorf("fileapi: export incomplete (no X-Export-Rows trailer)")
	}
	res.Rows, _ = strconv.ParseInt(rows, 10, 64)
	return res, nil
}
