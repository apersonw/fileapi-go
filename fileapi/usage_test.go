package fileapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestUsageAndTrafficExceeded(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"files": 3, "bytes": 3000, "quota_bytes": 0, "month": "2026-10", "traffic_quota_bytes": 5000,
			"traffic":                 map[string]any{"egress_bytes": 2500, "proxy_bytes": 500, "estimated_bytes": 2000, "downloads": 4, "upload_bytes": 3000, "uploads": 3},
			"alerts":                  map[string]string{"traffic": "warning", "transcode": "exceeded"},
			"transcode_quota_minutes": 60, "transcode_used_ms": 3_600_000})
	})
	f.handle("POST /open/v1/files/2/video/transcode", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusTooManyRequests, CodeTranscodeExceeded, "超出客户端本月转码分钟配额")
	})
	f.handle("GET /open/v1/files/1/download", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusTooManyRequests, CodeTrafficExceeded, "本月下载流量已用 5.0 KB，达到配额 4.9 KB，下月 1 日恢复")
	})
	c := f.client(t)
	ctx := context.Background()
	u, err := c.Usage(ctx)
	if err != nil || u.Files != 3 || u.Month != "2026-10" || u.TrafficQuotaBytes != 5000 ||
		u.Alerts["traffic"] != UsageWarning || u.Alerts["transcode"] != UsageExceeded || u.TranscodeQuotaMinutes != 60 || u.TranscodeUsedMS != 3_600_000 ||
		u.Traffic != (TrafficStats{EgressBytes: 2500, ProxyBytes: 500, EstimatedBytes: 2000, Downloads: 4, UploadBytes: 3000, Uploads: 3}) {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	var buf bytes.Buffer
	if _, err := c.Download(ctx, 1, &buf); !IsCode(err, CodeTrafficExceeded) || buf.Len() != 0 {
		t.Fatalf("download over quota err = %v", err)
	}
	if _, err := c.Transcode(ctx, 2); !IsCode(err, CodeTranscodeExceeded) {
		t.Fatalf("transcode over quota err = %v", err)
	}
}
