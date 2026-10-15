package fileapi

import (
	"context"
	"net/http"
	"time"
)

// 文档预览状态
const (
	PreviewPending = "pending"
	PreviewRunning = "running"
	PreviewReady   = "ready"
	PreviewFailed  = "failed"
)

// PreviewBrief 文件信息中的文档预览概况
type PreviewBrief struct {
	Status string `json:"status"`
	// Available 有可访问的预览 PDF（重新生成期间仍可访问旧的）
	Available bool `json:"available"`
}

// Preview Office 文档（Word / Excel / PPT / WPS / OpenDocument）转为 PDF 的状态
type Preview struct {
	Status    string     `json:"status"`
	Available bool       `json:"available"`
	Attempts  int        `json:"attempts"`
	Error     string     `json:"error,omitempty"`
	PDFBytes  int64      `json:"pdf_bytes"`
	ConvertMS int64      `json:"convert_ms"`
	ReadyAt   *time.Time `json:"ready_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Preview 文档预览状态；没有生成过预览时返回 404（IsNotFound）
func (c *Client) Preview(ctx context.Context, id int64) (*Preview, error) {
	var p Preview
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/preview"), idempotent: true}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// RequestPreview 生成或重新生成文档预览（服务端系统设置 preview.auto 开启时上传后会自动生成）；重新生成期间仍可访问旧的 PDF
func (c *Client) RequestPreview(ctx context.Context, id int64) (*Preview, error) {
	var p Preview
	if err := c.call(ctx, &request{method: http.MethodPost, path: filePath(id, "/preview")}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WaitPreview 轮询直到预览生成（ready）或失败，或 ctx 结束；interval 为 0 时每 2 秒查询一次
func (c *Client) WaitPreview(ctx context.Context, id int64, interval time.Duration) (*Preview, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		p, err := c.Preview(ctx, id)
		if err != nil || p.Status == PreviewReady || p.Status == PreviewFailed {
			return p, err
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-time.After(interval):
		}
	}
}
