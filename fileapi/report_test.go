package fileapi

import (
	"context"
	"net/http"
	"testing"
)

func TestUsageReport(t *testing.T) {
	f := newFakeServer(t)
	f.handle("GET /open/v1/usage/report", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") == "csv" {
			if q.Get("month") != "2026-09" {
				t.Errorf("csv query = %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = w.Write([]byte("\xef\xbb\xbf月份\n2026-09\n"))
			return
		}
		if q.Get("from") != "2026-07" || q.Get("to") != "2026-09" || q.Has("month") {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		ok(w, map[string]any{"from": "2026-07", "to": "2026-09", "timezone": "Asia/Shanghai", "rows": []map[string]any{
			{"month": "2026-09", "client_id": "fa_x", "days": 30, "bytes": 50, "avg_bytes": 115, "egress_bytes": 16, "transcode_minutes": 1.5},
		}})
	})
	c := f.client(t)
	ctx := context.Background()
	r, err := c.UsageReport(ctx, &UsageReportOptions{From: "2026-07", To: "2026-09"})
	if err != nil || len(r.Rows) != 1 || r.Rows[0].AvgBytes != 115 || r.Rows[0].TranscodeMinutes != 1.5 || r.Timezone != "Asia/Shanghai" {
		t.Fatalf("report = %+v, %v", r, err)
	}
	csv, err := c.UsageReportCSV(ctx, &UsageReportOptions{Month: "2026-09"})
	if err != nil || string(csv) != "\xef\xbb\xbf月份\n2026-09\n" {
		t.Fatalf("csv = %q, %v", csv, err)
	}
}
