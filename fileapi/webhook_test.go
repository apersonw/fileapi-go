package fileapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const vectorBody = `{"id":"0b0c6f3e-5d0e-4f7a-9d6e-2f1a3b4c5d6e","type":"file.uploaded","created_at":"2026-10-02T08:00:00+08:00","data":{"id":42}}`

// 与服务端（signWebhook）、Java SDK 共用的签名向量
const vectorSignature = "t=1790726400,v1=b5e08e04a586e55e13cf33f004caa388da70fc1492c44404febf994aba206552"

func signForTest(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifyWebhook(t *testing.T) {
	if signForTest("whsec_test_secret", 1790726400, []byte(vectorBody)) != vectorSignature {
		t.Fatal("signature vector mismatch")
	}
	// 向量的时间在过去，放宽偏差只为校验签名算法
	e, err := VerifyWebhook("whsec_test_secret", vectorSignature, []byte(vectorBody), 100*365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f, err := e.File()
	if err != nil || e.Type != EventFileUploaded || f.ID != 42 {
		t.Fatalf("event %+v file %+v %v", e, f, err)
	}

	now := time.Now().Unix()
	body := []byte(`{"id":"e1","type":"import.completed","created_at":"2026-10-02T00:00:00Z","data":{"id":3}}`)
	sig := signForTest("s", now, body)
	if _, err := VerifyWebhook("s", sig, body, 0); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		secret, sig string
		body        []byte
		want        error
	}{
		"wrong secret": {"x", sig, body, ErrWebhookSignature},
		"tampered":     {"s", sig, append([]byte{' '}, body...), ErrWebhookSignature},
		"missing":      {"s", "", body, ErrWebhookSignature},
		"expired":      {"s", signForTest("s", now-3600, body), body, ErrWebhookTimestamp},
		"rotation":     {"s", sig + ",v1=" + hex.EncodeToString(make([]byte, 32)), body, nil},
	}
	for name, c := range cases {
		if _, err := VerifyWebhook(c.secret, c.sig, c.body, 0); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if _, err := (&WebhookEvent{Type: EventImportCompleted}).File(); err == nil {
		t.Fatal("task event is not a file event")
	}
}

func TestWebhookHandler(t *testing.T) {
	var got *WebhookEvent
	h := WebhookHandler("s", func(_ *http.Request, e *WebhookEvent) error {
		got = e
		if e.ID == "fail" {
			return errors.New("busy")
		}
		return nil
	})
	post := func(id, secret string) int {
		body := []byte(`{"id":"` + id + `","type":"file.deleted","created_at":"2026-10-02T00:00:00Z","data":{}}`)
		req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(body))
		req.Header.Set("X-FileApi-Signature", signForTest(secret, time.Now().Unix(), body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("e1", "s"); code != http.StatusNoContent || got.ID != "e1" {
		t.Fatalf("valid: %d %+v", code, got)
	}
	if code := post("e2", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if code := post("fail", "s"); code != http.StatusInternalServerError {
		t.Fatalf("handler error: %d", code)
	}
}

func TestUsageAlertEvent(t *testing.T) {
	body := []byte(`{"id":"e2","type":"quota.traffic","created_at":"2026-10-13T00:00:00Z","data":{"client_id":"fa_x","client_name":"商城","kind":"traffic",` +
		`"level":"exceeded","threshold_percent":100,"used_bytes":1200,"quota_bytes":1000,"percent":120,"month":"2026-10","checked_at":"2026-10-13T00:00:00Z"}}`)
	e, err := VerifyWebhook("s", signForTest("s", time.Now().Unix(), body), body, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.UsageAlert()
	if err != nil || e.Type != EventQuotaTraffic || a.Level != UsageExceeded || a.UsedBytes != 1200 || a.Percent != 120 || a.Month != "2026-10" {
		t.Fatalf("alert = %+v, %v", a, err)
	}
	if _, err := e.File(); err == nil {
		t.Fatal("quota event is not a file event")
	}
	if _, err := (&WebhookEvent{Type: EventFileUploaded}).UsageAlert(); err == nil {
		t.Fatal("file event is not a usage alert")
	}
	tr := &WebhookEvent{Type: EventQuotaTranscode, Data: []byte(`{"kind":"transcode","level":"warning","used_bytes":0,"quota_bytes":0,` +
		`"used_minutes":48.5,"quota_minutes":60,"percent":80.8,"month":"2026-10"}`)}
	if a, err := tr.UsageAlert(); err != nil || a.Kind != "transcode" || a.UsedMinutes != 48.5 || a.QuotaMinutes != 60 || a.UsedBytes != 0 {
		t.Fatalf("transcode alert = %+v, %v", a, err)
	}
}
