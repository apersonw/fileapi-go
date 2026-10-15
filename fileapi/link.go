package fileapi

import (
	"context"
	"net/http"
	"net/url"
)

// LinkStatus 查询限次 / 可吊销链接的状态；链接已吊销、已过期或不属于本客户端时返回 IsNotFound 错误
func (c *Client) LinkStatus(ctx context.Context, linkID string) (*Link, error) {
	var l Link
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/links/" + url.PathEscape(linkID), idempotent: true}, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// RevokeLink 吊销链接：立即失效（之后访问返回 410 / CodeLinkRevoked），多实例即时生效
func (c *Client) RevokeLink(ctx context.Context, linkID string) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: "/open/v1/links/" + url.PathEscape(linkID), idempotent: true}, nil)
}
