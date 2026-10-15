package fileapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Webhook 事件类型
const (
	EventFileUploaded = "file.uploaded"
	EventFileDeleted  = "file.deleted"
	// EventFileQuarantined 文件未通过病毒扫描被隔离
	EventFileQuarantined = "file.quarantined"
	// EventFileRestored 回收站中的文件被恢复；EventFilePurged 回收站中的文件被彻底删除（到期或手动）
	EventFileRestored = "file.restored"
	EventFilePurged   = "file.purged"
	// EventFileExpiring / EventFileRetentionExpiring 文件即将到期删除 / 保留锁定即将到期（到期提醒，数据见 ExpiringFiles）
	EventFileExpiring          = "file.expiring"
	EventFileRetentionExpiring = "file.retention_expiring"
	// EventCollectionSubmitted 文件收集收到一次提交，数据见 CollectionSubmission
	EventCollectionSubmitted = "collection.submitted"
	// EventQuotaStorage / EventQuotaTraffic / EventQuotaTranscode 存储用量 / 本月下载流量 / 本月转码分钟达到配额的预警比例或配额
	// （用量告警，数据见 UsageAlert）
	EventQuotaStorage   = "quota.storage"
	EventQuotaTraffic   = "quota.traffic"
	EventQuotaTranscode = "quota.transcode"
	// EventVideoReady / EventVideoFailed 视频转码完成 / 失败（不再自动重试），数据见 VideoEvent
	EventVideoReady         = "video.ready"
	EventVideoFailed        = "video.failed"
	EventMigrationCompleted = "migration.completed"
	EventMigrationFailed    = "migration.failed"
	EventImportCompleted    = "import.completed"
	EventImportFailed       = "import.failed"
	EventReconcileCompleted = "reconcile.completed"
	EventReconcileFailed    = "reconcile.failed"
	EventWebhookTest        = "webhook.test"
)

// DefaultWebhookTolerance 签名时间与本机时间允许的偏差
const DefaultWebhookTolerance = 5 * time.Minute

var (
	// ErrWebhookSignature 签名缺失或不匹配
	ErrWebhookSignature = errors.New("fileapi: invalid webhook signature")
	// ErrWebhookTimestamp 签名时间超出允许偏差（可能是重放）
	ErrWebhookTimestamp = errors.New("fileapi: webhook timestamp outside tolerance")
)

// WebhookEvent FileApi 推送的事件。同一事件可能重复推送（至少投递一次），请按 ID 去重；不保证顺序
type WebhookEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// WebhookFile 文件事件（file.uploaded / file.deleted）的数据
type WebhookFile struct {
	ID               int64             `json:"id"`
	ClientID         string            `json:"client_id"`
	OriginalFilename string            `json:"original_filename"`
	ContentType      string            `json:"content_type"`
	Size             int64             `json:"size"`
	Category         string            `json:"category"`
	SHA256           string            `json:"sha256"`
	ObjectType       string            `json:"object_type"`
	ObjectID         string            `json:"object_id"`
	Metadata         map[string]string `json:"metadata"`
	// Trashed file.deleted 事件中为 true 表示移入了回收站（PurgeAt 之后彻底删除，之前可能被恢复）
	Trashed   bool       `json:"trashed"`
	PurgeAt   *time.Time `json:"purge_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// File 解析文件事件的数据
func (e *WebhookEvent) File() (*WebhookFile, error) {
	if !strings.HasPrefix(e.Type, "file.") || e.Type == EventFileExpiring || e.Type == EventFileRetentionExpiring {
		return nil, fmt.Errorf("fileapi: %s is not a file event", e.Type)
	}
	var f WebhookFile
	if err := json.Unmarshal(e.Data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// 用量告警级别
const (
	// UsageWarning 达到预警比例（服务端系统设置 usage_alert.warn_percent，默认 80%）
	UsageWarning = "warning"
	// UsageExceeded 达到配额
	UsageExceeded = "exceeded"
)

// ExpiringFiles 到期提醒事件（file.expiring / file.retention_expiring）的数据：到期时间在 DaysBefore 天内的文件（每个事件最多 100 个），
// 每个到期时间只提醒一次，续期后再次进入提醒窗口时重新提醒
type ExpiringFiles struct {
	ClientID string `json:"client_id"`
	// Kind expiry 到期后自动删除 | retention 保留锁定到期
	Kind       string         `json:"kind"`
	DaysBefore int            `json:"days_before"`
	Files      []ExpiringFile `json:"files"`
	CheckedAt  time.Time      `json:"checked_at"`
}

// ExpiringFile 即将到期的文件：ExpiresAt 仅 file.expiring，RetainUntil 仅 file.retention_expiring
type ExpiringFile struct {
	ID               int64      `json:"id"`
	OriginalFilename string     `json:"original_filename"`
	Folder           string     `json:"folder"`
	Size             int64      `json:"size"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RetainUntil      *time.Time `json:"retain_until,omitempty"`
}

// CollectionSubmission 文件收集提交事件（collection.submitted）的数据：一次提交中上传成功的文件（至少一个）与失败的文件数
type CollectionSubmission struct {
	ClientID string `json:"client_id"`
	Code     string `json:"code"`
	Title    string `json:"title"`
	// Folder 文件放入的目录（按提交者分子目录时为子目录）
	Folder string `json:"folder"`
	// Uploader / Note 提交者填写的姓名与备注
	Uploader    string           `json:"uploader,omitempty"`
	Note        string           `json:"note,omitempty"`
	Files       []CollectionFile `json:"files"`
	Failed      int              `json:"failed"`
	SubmittedAt time.Time        `json:"submitted_at"`
}

// CollectionFile 收集到的文件
type CollectionFile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// UsageAlert 用量告警事件（quota.storage / quota.traffic / quota.transcode）的数据：每个级别只推送一次，用量回落后再次达到时重新推送，
// 流量与转码每月重新开始
type UsageAlert struct {
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
	// Kind storage 存储用量 | traffic 本月下载流量 | transcode 本月转码分钟
	Kind string `json:"kind"`
	// Level UsageWarning | UsageExceeded
	Level string `json:"level"`
	// ThresholdPercent 触发的阈值（预警比例或 100）
	ThresholdPercent int `json:"threshold_percent"`
	// UsedBytes / QuotaBytes 存储与流量的用量与配额（字节），quota.transcode 为 0
	UsedBytes  int64   `json:"used_bytes"`
	QuotaBytes int64   `json:"quota_bytes"`
	Percent    float64 `json:"percent"`
	// UsedMinutes / QuotaMinutes 本月已用的转码分钟（一位小数）与配额，仅 quota.transcode
	UsedMinutes  float64 `json:"used_minutes,omitempty"`
	QuotaMinutes int     `json:"quota_minutes,omitempty"`
	// Month 流量 / 转码所属自然月（YYYY-MM），仅 quota.traffic / quota.transcode
	Month     string    `json:"month,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// UsageAlert 解析用量告警事件的数据
func (e *WebhookEvent) UsageAlert() (*UsageAlert, error) {
	if !strings.HasPrefix(e.Type, "quota.") {
		return nil, fmt.Errorf("fileapi: %s is not a usage alert event", e.Type)
	}
	var a UsageAlert
	if err := json.Unmarshal(e.Data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Expiring 解析到期提醒事件（file.expiring / file.retention_expiring）的数据
func (e *WebhookEvent) Expiring() (*ExpiringFiles, error) {
	if e.Type != EventFileExpiring && e.Type != EventFileRetentionExpiring {
		return nil, fmt.Errorf("fileapi: %s is not an expiry alert event", e.Type)
	}
	var v ExpiringFiles
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// CollectionSubmission 解析文件收集提交事件（collection.submitted）的数据
func (e *WebhookEvent) CollectionSubmission() (*CollectionSubmission, error) {
	if e.Type != EventCollectionSubmitted {
		return nil, fmt.Errorf("fileapi: %s is not a collection event", e.Type)
	}
	var v CollectionSubmission
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VideoEvent 视频转码事件（video.ready / video.failed）的数据
type VideoEvent struct {
	AssetID  int64  `json:"asset_id"`
	ClientID string `json:"client_id"`
	Status   string `json:"status"`
	// FileIDs 引用该视频对象的文件（秒传共用对象时有多个，含回收站中的）
	FileIDs     []int64   `json:"file_ids"`
	Renditions  []string  `json:"renditions"`
	DurationMS  int64     `json:"duration_ms"`
	Bytes       int64     `json:"bytes"`
	TranscodeMS int64     `json:"transcode_ms"`
	Error       string    `json:"error"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Video 解析视频转码事件的数据
func (e *WebhookEvent) Video() (*VideoEvent, error) {
	if !strings.HasPrefix(e.Type, "video.") {
		return nil, fmt.Errorf("fileapi: %s is not a video event", e.Type)
	}
	var v VideoEvent
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VerifyWebhook 校验请求头 X-FileApi-Signature（t={Unix 秒},v1={hex(HMAC-SHA256(secret, "{t}.{body}"))}）与签名时间，
// 通过后解析事件。tolerance 为 0 时使用 DefaultWebhookTolerance。body 须为原始请求体（未经重新序列化）。
func VerifyWebhook(secret, signature string, body []byte, tolerance time.Duration) (*WebhookEvent, error) {
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	var ts int64
	var sigs [][]byte
	for _, part := range strings.Split(signature, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return nil, ErrWebhookSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	expected := mac.Sum(nil)
	matched := false
	for _, s := range sigs {
		matched = hmac.Equal(s, expected) || matched
	}
	if !matched {
		return nil, ErrWebhookSignature
	}
	if d := time.Since(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return nil, ErrWebhookTimestamp
	}
	var e WebhookEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("fileapi: decode webhook event: %w", err)
	}
	return &e, nil
}

// WebhookHandler 返回校验签名后调用 fn 的 http.Handler：签名无效返回 401；fn 返回错误时返回 500，FileApi 会按退避重试。
//
//	http.Handle("/fileapi/events", fileapi.WebhookHandler(secret, func(r *http.Request, e *fileapi.WebhookEvent) error {
//		if e.Type == fileapi.EventFileUploaded { f, _ := e.File(); ... }
//		return nil
//	}))
func WebhookHandler(secret string, fn func(r *http.Request, e *WebhookEvent) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		e, err := VerifyWebhook(secret, r.Header.Get("X-FileApi-Signature"), body, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		if err := fn(r, e); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
