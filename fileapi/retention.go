package fileapi

import (
	"context"
	"net/http"
	"time"
)

// 文件保留锁定（合规保留 / 法律保留）：锁定期内（LegalHold，或 RetainUntil 未到）的文件不能删除（含移入回收站）、
// 不能上传新版本、不能恢复或删除历史版本，返回 CodeFileLocked（42302）；服务端的到期清理与生命周期删除规则也会跳过它们。
// 改名、移动目录、修改元数据与访问不受影响。客户端只能设置或延长保留期限（缩短返回 CodeConflict），可以设置或解除法律保留；
// 缩短或清除保留期限需要管理员操作。客户端配置了 retention_days 时，上传的文件自动带保留期限。

// RetentionOptions 保留设置的修改，nil 的字段不变
type RetentionOptions struct {
	// RetainUntil 保留期限（须晚于当前时间，最长 100 年）：只能设置或延长
	RetainUntil *time.Time
	// LegalHold 设置（true）或解除（false）法律保留
	LegalHold *bool
}

// SetRetention 修改文件的保留设置，返回文件的最新信息
func (c *Client) SetRetention(ctx context.Context, id int64, opts RetentionOptions) (*File, error) {
	body := map[string]any{}
	if opts.RetainUntil != nil {
		body["retain_until"] = opts.RetainUntil.UTC().Format(time.RFC3339)
	}
	if opts.LegalHold != nil {
		body["legal_hold"] = *opts.LegalHold
	}
	var f File
	req := &request{method: http.MethodPut, path: filePath(id, "/retention"), json: jsonBody(body), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
