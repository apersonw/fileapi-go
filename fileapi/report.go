package fileapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 月度用量报表（月度账单）：按自然月（服务端 traffic.timezone）汇总本客户端的存储、上传与删除、下载流量与转码用量。

// UsageReportOptions 报表的月份（YYYY-MM）：Month 单月，或 From / To 范围（含两端，最多 12 个月）；都为空时为本月
type UsageReportOptions struct {
	Month string
	From  string
	To    string
}

func (o *UsageReportOptions) query(format string) url.Values {
	v := url.Values{}
	if o != nil {
		for k, val := range map[string]string{"month": o.Month, "from": o.From, "to": o.To} {
			if val != "" {
				v.Set(k, val)
			}
		}
	}
	if format != "" {
		v.Set("format", format)
	}
	return v
}

// UsageReportRow 某月的用量
type UsageReportRow struct {
	Month    string `json:"month"`
	ClientID string `json:"client_id"`
	// Days 统计的天数，Partial 本月尚未结束（只统计到今天）
	Days    int  `json:"days"`
	Partial bool `json:"partial,omitempty"`
	// Files / Bytes 月末的文件数与总大小，PeakBytes 月内峰值，AvgBytes 按天平均（按 GB·月 计费用）
	Files       int64 `json:"files"`
	Bytes       int64 `json:"bytes"`
	PeakBytes   int64 `json:"peak_bytes"`
	AvgBytes    int64 `json:"avg_bytes"`
	Uploads     int64 `json:"uploads"`
	UploadBytes int64 `json:"upload_bytes"`
	Deletes     int64 `json:"deletes"`
	DeleteBytes int64 `json:"delete_bytes"`
	// EgressBytes 下载流量（与流量配额一致）= ProxyBytes（代理返回）+ EstimatedBytes（跳转与直链估算）
	Downloads        int64   `json:"downloads"`
	ProxyBytes       int64   `json:"proxy_bytes"`
	EstimatedBytes   int64   `json:"estimated_bytes"`
	EgressBytes      int64   `json:"egress_bytes"`
	TranscodeJobs    int64   `json:"transcode_jobs"`
	TranscodeMS      int64   `json:"transcode_ms"`
	TranscodeMinutes float64 `json:"transcode_minutes"`
	// 当前的配额，0 表示不限
	QuotaBytes            int64 `json:"quota_bytes"`
	TrafficQuotaBytes     int64 `json:"traffic_quota_bytes"`
	TranscodeQuotaMinutes int   `json:"transcode_quota_minutes"`
}

// UsageReport 月度用量报表
type UsageReport struct {
	From        string           `json:"from"`
	To          string           `json:"to"`
	Timezone    string           `json:"timezone"`
	GeneratedAt time.Time        `json:"generated_at"`
	Rows        []UsageReportRow `json:"rows"`
}

// UsageReport 本客户端的月度用量；opts 为 nil 时为本月
func (c *Client) UsageReport(ctx context.Context, opts *UsageReportOptions) (*UsageReport, error) {
	var r UsageReport
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/usage/report", query: opts.query(""), idempotent: true}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UsageReportCSV 月度用量报表的 CSV（UTF-8 带 BOM，中文表头，Excel 可直接打开）
func (c *Client) UsageReportCSV(ctx context.Context, opts *UsageReportOptions) ([]byte, error) {
	resp, err := c.send(ctx, &request{method: http.MethodGet, path: "/open/v1/usage/report", query: opts.query("csv"), idempotent: true})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
