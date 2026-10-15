package fileapi

import "time"

// File 文件信息
type File struct {
	ID int64 `json:"id"`
	// URL 仅公有存储返回存储公开地址（存储开启 CDN URL 鉴权时不返回）；推荐用 AccessURL 换取统一入口链接
	URL              string `json:"url,omitempty"`
	Filename         string `json:"filename"`
	OriginalFilename string `json:"original_filename"`
	Ext              string `json:"ext"`
	ContentType      string `json:"content_type"`
	Size             int64  `json:"size"`
	FormatSize       string `json:"format_size"`
	// Category IMAGE | AUDIO | VIDEO | DOCUMENT | OTHER
	Category string `json:"category"`
	// SHA256 由服务端计算（代理上传、秒传；服务端开启直传回读时也包括直传），否则为空
	SHA256       string            `json:"sha256,omitempty"`
	ObjectType   string            `json:"object_type,omitempty"`
	ObjectID     string            `json:"object_id,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	HasThumbnail bool              `json:"has_thumbnail"`
	// Status 0 待确认（直传未完成）| 1 正常 | 3 回收站（只在 ListTrash 中出现）
	Status int `json:"status"`
	// ExpiresAt 过期时间，到期后服务端自动删除；nil 表示不过期
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// ScanStatus 病毒扫描状态（ScanPending / ScanClean / ScanSkipped / ScanFailed），未扫描时为空；ScanDetail 为原因
	ScanStatus string `json:"scan_status,omitempty"`
	ScanDetail string `json:"scan_detail,omitempty"`
	// Width / Height 图片（按 EXIF 方向校正后）与视频的宽高，DurationMS 音视频时长（毫秒）；未知时为 nil。
	// 生成了缩略图的图片上传时即返回，其余由服务端在上传后补齐
	Width      *int   `json:"width,omitempty"`
	Height     *int   `json:"height,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	// TrashedAt 移入回收站的时间，PurgeAt 到期彻底删除的时间，只有回收站中的文件有值
	TrashedAt *time.Time `json:"trashed_at,omitempty"`
	PurgeAt   *time.Time `json:"purge_at,omitempty"`
	// Version 当前版本号（从 1 开始，每上传一个新版本或恢复一个历史版本加 1）
	Version int `json:"version"`
	// Tags 文件的标签（按名称排序，见 SetFileTags）
	Tags []TagBrief `json:"tags,omitempty"`
	// RetainUntil 保留期限、LegalHold 法律保留（见 SetRetention）；Locked 当前是否处于保留锁定期
	RetainUntil *time.Time `json:"retain_until,omitempty"`
	LegalHold   bool       `json:"legal_hold,omitempty"`
	Locked      bool       `json:"locked,omitempty"`
	// AccessCount / LastAccessedAt 经统一访问入口与代理下载的累计访问次数与最近访问时间（Get / List 返回，从未访问过时为零值）
	AccessCount    int64      `json:"access_count,omitempty"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	// Folder 所在目录（虚拟目录），以 / 开头和结尾，根目录为 /
	Folder string `json:"folder"`
	// Video 视频转码状态（发起过转码的视频才有）
	Video *VideoBrief `json:"video,omitempty"`
	// Preview Office 文档预览状态（登记过预览的文档才有），Available 时可用 URLOptions.Preview 访问预览 PDF
	Preview   *PreviewBrief `json:"preview,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// 文件状态
const (
	FileStatusPending = 0
	FileStatusActive  = 1
	// FileStatusTrashed 回收站：已删除，到期前可以恢复
	FileStatusTrashed = 3
)

// 病毒扫描状态（发现病毒的文件被隔离，客户端查询不到）
const (
	ScanPending = "pending"
	ScanClean   = "clean"
	ScanSkipped = "skipped"
	ScanFailed  = "failed"
)

// 文件分类
const (
	CategoryImage    = "IMAGE"
	CategoryAudio    = "AUDIO"
	CategoryVideo    = "VIDEO"
	CategoryDocument = "DOCUMENT"
	CategoryOther    = "OTHER"
)

// UploadResult 代理上传结果
type UploadResult struct {
	File
	// Reused 命中同一业务对象的已有文件（未上传新内容）
	Reused bool `json:"reused"`
	// Deduplicated 内容与本客户端已有文件相同，服务端复用了已有对象（不额外占用存储）
	Deduplicated bool `json:"deduplicated"`
	// Instant 由 TryInstant 秒传完成，未传输文件内容
	Instant bool `json:"-"`
}

// 列表排序
const (
	SortCreatedAt        = "created_at"
	SortSize             = "size"
	SortOriginalFilename = "original_filename"

	OrderAsc  = "asc"
	OrderDesc = "desc"
)

// ListQuery 文件列表查询，零值字段不参与过滤
type ListQuery struct {
	Category string
	// OriginalFilename 原始文件名模糊匹配
	OriginalFilename string
	ObjectType       string
	ObjectID         string
	StartTime        time.Time
	EndTime          time.Time
	// Sort 默认 created_at；Order 默认 desc
	Sort  string
	Order string
	// PageSize 每页条数（1~100），默认 20
	PageSize int
	// Cursor 上一页的 NextCursor，从该位置之后续取；排序参数须与上一页一致
	Cursor string
	// Page 按页码翻页（数据量大时靠后的页较慢，推荐游标翻页）；设置 Cursor 时忽略
	Page int
	// WithTotal 游标翻页时也统计总数（按页码翻页总会统计）
	WithTotal bool
	// Metadata 按元数据筛选：文件须包含其中全部键值；MetadataKeys 文件须包含这些键（值不限）
	Metadata     map[string]string
	MetadataKeys []string
	// 按媒体信息筛选（宽高为像素，时长为毫秒，0 表示不限），只返回已提取到该信息的文件
	MinWidth, MaxWidth, MinHeight, MaxHeight int
	MinDurationMS, MaxDurationMS             int64
	// Folder 只列出该目录中的文件（如 "/合同/2026"），Recursive 同时包含下级目录
	Folder    string
	Recursive bool
	// Locked 非 nil 时按是否处于保留锁定期筛选
	Locked *bool
	// TagIDs 按标签筛选（最多 20 个）：默认须包含全部标签，TagAny 时包含任一即可
	TagIDs []int64
	TagAny bool
	// ExpiringDays 只看 N 天内到期删除的文件；RetentionExpiringDays 只看 N 天内保留锁定到期的文件（法律保留的除外）
	ExpiringDays          int
	RetentionExpiringDays int
}

// FilePage 文件列表的一页
type FilePage struct {
	List []File `json:"list"`
	// Total 仅在按页码翻页或 WithTotal 时返回
	Total      *int64 `json:"total,omitempty"`
	Page       int    `json:"page,omitempty"`
	PageSize   int    `json:"page_size"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// BatchDeleteResult 批量删除结果
type BatchDeleteResult struct {
	Deleted []int64 `json:"deleted"`
	// Trashed Deleted 中移入回收站的文件（其余已彻底删除）
	Trashed []int64 `json:"trashed"`
	// Failed 删除失败的文件及原因
	Failed map[int64]string `json:"failed"`
}

// 访问链接模式
const (
	// ModeGateway 统一访问入口链接（FileApi 域名下的 /a/{token}），与底层存储解耦，默认
	ModeGateway = "gateway"
	// ModeDirect 存储平台直链：公有桶公开地址或私有桶预签名地址
	ModeDirect = "direct"
)

// URLOptions 访问链接选项
type URLOptions struct {
	// Mode ModeGateway（默认）| ModeDirect
	Mode string
	// Expires 有效期，0 使用服务端默认值；按秒向上取整
	Expires time.Duration
	// Permanent 长期有效（仅公有存储的统一入口链接），优先于 Expires
	Permanent bool
	// Download 以附件形式下载（文件名为原始文件名）
	Download bool
	// Thumbnail 返回缩略图的地址（文件需有缩略图）
	Thumbnail bool
	// Preview 返回 Office 文档预览 PDF 的地址（须已生成，见 Preview / RequestPreview），浏览器内直接打开；不能与 Thumbnail、Image 同时使用
	Preview bool
	// Image 图片处理参数（仅 JPEG / PNG / GIF / WebP），处理结果由服务端缓存；不能与 Thumbnail 同时使用
	Image *ImageOptions
	// MaxVisits 限定访问次数（仅统一入口链接，须有有效期）：完整下载或从头开始的分段请求计一次，Range 续传与 HEAD 不计
	MaxVisits int64
	// Revocable 可吊销（仅统一入口链接，须有有效期），之后可用 RevokeLink 让链接立即失效；MaxVisits > 0 的链接总是可吊销
	Revocable bool
	// Password 提取码（4 ~ 16 位字母或数字，不区分大小写；仅统一入口链接，须有有效期，链接可吊销）：浏览器打开时先输入提取码，
	// 程序访问在 LinkPasswordHeader 请求头中携带
	Password string
}

// LinkPasswordHeader 访问带提取码的链接时携带提取码的请求头
const LinkPasswordHeader = "X-Link-Password"

// ImageOptions 图片处理参数，零值字段表示不限 / 默认
type ImageOptions struct {
	// Width / Height 输出宽高（不放大）
	Width  int
	Height int
	// Fit FitContain（默认）等比缩放到宽高以内 | FitCover 居中裁剪填满（需同时指定宽高）
	Fit string
	// Format FormatJPEG | FormatPNG | FormatWebP；不指定时不透明图片输出 JPEG、带透明通道输出 PNG
	Format string
	// Quality JPEG / WebP 质量 1~100，默认 82
	Quality int
	// Watermark 水印，叠加在缩放 / 裁剪后的图片上；可单独使用（只设置 Watermark）
	Watermark *Watermark
}

// Watermark 水印参数：Text 与 ImageID 二选一，其余零值字段表示默认
type Watermark struct {
	// Text 文字水印：单行，最多 30 个字符；中文等非西文字符需服务端配置 image.watermark_font（Docker 镜像已内置）
	Text string
	// ImageID 图片水印：本客户端的 JPEG / PNG / GIF / WebP 图片文件 ID（不超过 2MB，建议带透明通道的 PNG）
	ImageID int64
	// Position 位置：PositionSE（默认）等九宫格位置，或 PositionTile 倾斜平铺
	Position string
	// Opacity 不透明度 1~100，默认 100
	Opacity int
	// Size 文字字号或图片宽度占输出图片宽度的百分比 1~100，默认文字 4、图片 20
	Size int
	// Color 文字颜色，6 位十六进制如 "ff0000"，默认白色
	Color string
}

// 水印位置
const (
	PositionNW     = "nw"
	PositionN      = "n"
	PositionNE     = "ne"
	PositionW      = "w"
	PositionCenter = "c"
	PositionE      = "e"
	PositionSW     = "sw"
	PositionS      = "s"
	PositionSE     = "se"
	// PositionTile 倾斜 30° 平铺满整张图片
	PositionTile = "tile"
)

// 图片处理选项
const (
	FitContain = "contain"
	FitCover   = "cover"
	FormatJPEG = "jpeg"
	FormatPNG  = "png"
	// FormatWebP 有损 WebP（保留透明通道），同等质量下通常比 JPEG 更小
	FormatWebP = "webp"
)

// AccessURL 访问链接
type AccessURL struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
	// ExpiresAt 过期时间，为空表示长期有效
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// LinkID 限次 / 可吊销链接的标识，用于 LinkStatus / RevokeLink；普通链接为空
	LinkID string `json:"link_id,omitempty"`
	// MaxVisits 访问次数上限
	MaxVisits int64 `json:"max_visits,omitempty"`
	// PasswordProtected 链接需要提取码
	PasswordProtected bool `json:"password_protected,omitempty"`
}

// Link 限次 / 可吊销链接的状态
type Link struct {
	LinkID string `json:"link_id"`
	FileID int64  `json:"file_id"`
	// MaxVisits 访问次数上限，0 表示不限次数
	MaxVisits int64 `json:"max_visits"`
	// Visits 已计数的访问次数
	Visits int64 `json:"visits"`
	// PasswordProtected 链接需要提取码
	PasswordProtected bool      `json:"password_protected"`
	ExpiresAt         time.Time `json:"expires_at"`
}

// Usage 客户端用量：存储用量与本月流量
type Usage struct {
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
	// QuotaBytes 存储配额，0 表示不限制
	QuotaBytes int64 `json:"quota_bytes"`
	// Month 统计的自然月（服务端 traffic.timezone），如 2026-10
	Month   string       `json:"month"`
	Traffic TrafficStats `json:"traffic"`
	// TrafficQuotaBytes 每自然月的下载流量配额，0 表示不限制；达到后下载与访问返回 CodeTrafficExceeded
	TrafficQuotaBytes int64 `json:"traffic_quota_bytes"`
	// Alerts 当前的用量告警级别："storage" / "traffic" / "transcode" → UsageWarning | UsageExceeded（服务端定期检查），正常时为空
	Alerts map[string]string `json:"alerts,omitempty"`
	// VideoBytes Bytes 中的视频转码产物字节（计入存储配额）
	VideoBytes int64 `json:"video_bytes"`
	// TranscodeQuotaMinutes 每自然月的转码分钟配额，0 表示不限制；用完后发起转码返回 CodeTranscodeExceeded
	TranscodeQuotaMinutes int `json:"transcode_quota_minutes"`
	// TranscodeUsedMS 本月已用的转码时长（毫秒，源视频时长 × 输出档位数，转码成功时计入）
	TranscodeUsedMS int64 `json:"transcode_used_ms"`
}

// TrafficStats 流量合计。EgressBytes（下载流量，流量配额按它计算）= ProxyBytes（经 FileApi 代理返回的实际字节）
// + EstimatedBytes（统一入口 302 跳转与签发直链时按对象大小估算的字节）
type TrafficStats struct {
	EgressBytes    int64 `json:"egress_bytes"`
	ProxyBytes     int64 `json:"proxy_bytes"`
	EstimatedBytes int64 `json:"estimated_bytes"`
	Downloads      int64 `json:"downloads"`
	UploadBytes    int64 `json:"upload_bytes"`
	Uploads        int64 `json:"uploads"`
}

// MultipartStatus 分片直传进度（断点续传）
type MultipartStatus struct {
	File      File  `json:"file"`
	PartSize  int64 `json:"part_size"`
	PartCount int   `json:"part_count"`
	// Uploaded 已上传且大小正确的分片（按分片号升序）
	Uploaded      []UploadedPart `json:"uploaded"`
	UploadedBytes int64          `json:"uploaded_bytes"`
	// ExpiresAt 未完成的上传在此之后被服务端清理，需在此之前完成
	ExpiresAt time.Time `json:"expires_at"`
}

// UploadedPart 已上传的分片
type UploadedPart struct {
	PartNumber int    `json:"part_number"`
	Size       int64  `json:"size"`
	ETag       string `json:"etag"`
}
