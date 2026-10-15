package fileapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// 视频转码状态
const (
	VideoPending  = "pending"
	VideoRunning  = "running"
	VideoReady    = "ready"
	VideoFailed   = "failed"
	VideoCanceled = "canceled"
)

// VideoBrief 文件信息中附带的转码状态
type VideoBrief struct {
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	// Playable 有可播放的转码产物（重新转码期间仍可播放上一次的产物）
	Playable bool `json:"playable"`
}

// 视频编码（TranscodeOptions.Codecs，可并存）与音视频分离时音频输出的编码
const (
	CodecH264 = "h264"
	CodecHEVC = "hevc"
	CodecAV1  = "av1"
	CodecAAC  = "aac"
)

// Rendition 一档清晰度：H.264 的名称为档位名，HEVC / AV1 加 -hevc / -av1；音视频分离时音频为 "audio"（Codec 为 CodecAAC，宽高为 0）
type Rendition struct {
	Name string `json:"name"`
	// Codec 输出的编码（较早的、音视频合在一起的产物为空）
	Codec  string `json:"codec,omitempty"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Bandwidth 峰值码率（bit/s），AverageBandwidth 平均码率（bit/s，按分片实测；较早的产物为 0）
	Bandwidth        int   `json:"bandwidth"`
	AverageBandwidth int   `json:"average_bandwidth"`
	Segments         int   `json:"segments"`
	Bytes            int64 `json:"bytes"`
}

// Thumbnails 拖动预览雪碧图：每 IntervalMS 一格（Width×Height），每张 Columns×Columns 格，共 Count 格、Sheets 张；Bytes 计入存储配额
type Thumbnails struct {
	IntervalMS int64 `json:"interval_ms"`
	Width      int   `json:"width"`
	Height     int   `json:"height"`
	Columns    int   `json:"columns"`
	Count      int   `json:"count"`
	Sheets     int   `json:"sheets"`
	Bytes      int64 `json:"bytes"`
}

// Video 视频转码状态与产物
type Video struct {
	AssetID int64 `json:"asset_id"`
	// Status VideoPending 等；Stage 运行中的阶段：download / transcode / upload
	Status   string   `json:"status"`
	Stage    string   `json:"stage,omitempty"`
	Progress int      `json:"progress"`
	Profiles []string `json:"profiles"`
	// Codecs 请求的视频编码；Watermark 烧录的水印（没有时为 nil）
	Codecs    []string        `json:"codecs"`
	Watermark *VideoWatermark `json:"watermark,omitempty"`
	Playable  bool            `json:"playable"`
	// Dash 当前产物可以 DASH 播放（音视频分离且未加密），Play 返回 DashURL
	Dash bool `json:"dash"`
	// DurationMS / Width / Height 源视频时长与显示分辨率；Bytes 可播放产物总字节（计入存储配额）
	DurationMS  int64       `json:"duration_ms"`
	Width       int         `json:"width"`
	Height      int         `json:"height"`
	Bytes       int64       `json:"bytes"`
	TranscodeMS int64       `json:"transcode_ms"`
	Attempts    int         `json:"attempts"`
	Error       string      `json:"error,omitempty"`
	Renditions  []Rendition `json:"renditions"`
	// Encrypted 当前可播放产物的媒体分片经 HLS AES-128 加密（密钥由播放地址同一令牌的 /key 下发，播放器自动获取）
	Encrypted bool `json:"encrypted"`
	// Thumbnails 拖动预览雪碧图（服务端开启 video.thumbnails 时随转码生成），没有时为 nil
	Thumbnails *Thumbnails `json:"thumbnails,omitempty"`
	ReadyAt    *time.Time  `json:"ready_at,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Done 转码已结束（完成、失败或取消）
func (v *Video) Done() bool {
	return v.Status == VideoReady || v.Status == VideoFailed || v.Status == VideoCanceled
}

// PlayOptions 签发播放地址的选项（可为 nil）
type PlayOptions struct {
	// Expires 有效期（按秒向上取整，至少 60 秒），0 时取服务端默认有效期与"时长 × 2"的较大值
	Expires time.Duration
	// MaxVisits 限定播放次数（按主清单请求计次）；Revocable 签发可吊销的地址（用 RevokeLink 吊销）
	MaxVisits int64
	Revocable bool
	// Password 提取码（4 ~ 16 位字母或数字）：返回的地址须由播放页 POST {地址所在目录}/unlock 解锁后播放
	Password string
}

// Play 播放信息
type Play struct {
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Playable bool   `json:"playable"`
	// URL HLS 主清单地址（Playable 时返回），hls.js / Safari / iOS / Android ExoPlayer 可直接播放
	URL string `json:"url,omitempty"`
	// DashURL DASH 清单地址（.../manifest.mpd，与 URL 同一令牌），产物可 DASH 播放时返回（dash.js / Shaka / ExoPlayer）
	DashURL    string      `json:"dash_url,omitempty"`
	ExpiresAt  *time.Time  `json:"expires_at,omitempty"`
	LinkID     string      `json:"link_id,omitempty"`
	MaxVisits  int64       `json:"max_visits,omitempty"`
	DurationMS int64       `json:"duration_ms"`
	Renditions []Rendition `json:"renditions"`
	// PasswordProtected 地址带提取码：URL 不能直接播放，播放页先 POST {URL 所在目录}/unlock（{"password": "…"}）换取解锁后的地址
	PasswordProtected bool `json:"password_protected,omitempty"`
	// Subtitles 字幕轨（有可播放产物时由主清单的 SUBTITLES 分组提供，播放器可切换）
	Subtitles []Subtitle `json:"subtitles"`
	// ThumbnailsURL 拖动预览缩略图的 WebVTT 索引（.../thumbs.vtt，每条为雪碧图地址#xywh=x,y,w,h），生成了雪碧图时返回
	ThumbnailsURL string `json:"thumbnails_url,omitempty"`
	// FallbackURL 尚无可播放产物时原文件的统一入口链接
	FallbackURL string `json:"fallback_url,omitempty"`
}

func videoPath(id int64, suffix string) string { return filePath(id, "/video"+suffix) }

// Video 视频转码状态与各档清晰度；没有发起过转码时返回 IsNotFound 错误
func (c *Client) Video(ctx context.Context, id int64) (*Video, error) {
	var v Video
	if err := c.call(ctx, &request{method: http.MethodGet, path: videoPath(id, ""), idempotent: true}, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VideoWatermark 转码时烧录进画面的水印：Text 或本客户端的图片文件 ImageFileID（PNG / JPEG / WebP，不超过 5 MB）二选一
type VideoWatermark struct {
	Text        string `json:"text,omitempty"`
	ImageFileID int64  `json:"image_file_id,omitempty"`
	// Position 位置：nw / ne / sw / se（默认）/ center；Opacity 不透明度 10 ~ 100（默认 60）；Size 高度占画面短边的百分比 2 ~ 30（默认 5）
	Position string `json:"position,omitempty"`
	Opacity  int    `json:"opacity,omitempty"`
	Size     int    `json:"size,omitempty"`
}

// TranscodeOptions 发起转码的选项（可为 nil）
type TranscodeOptions struct {
	// Profiles 档位（如 "720p"），为空用服务端默认，超过源分辨率的自动跳过
	Profiles []string
	// Codecs 视频编码（CodecH264 / CodecHEVC / CodecAV1，可多选并存，每种编码各输出一套档位），为空用服务端设置 video.codecs
	Codecs []string
	// Watermark 烧录的水印，nil 时使用客户端的默认水印；NoWatermark 为 true 时不加水印
	Watermark   *VideoWatermark
	NoWatermark bool
}

// Transcode 发起或重新转码（档位为空用服务端默认，超过源分辨率的自动跳过）：重新转码完成前仍可播放旧产物；
// 正在转码时返回 CodeConflict 错误，不是视频文件返回 CodeBadRequest。指定编码与水印见 TranscodeWith
func (c *Client) Transcode(ctx context.Context, id int64, profiles ...string) (*Video, error) {
	return c.TranscodeWith(ctx, id, &TranscodeOptions{Profiles: profiles})
}

// TranscodeWith 按选项发起或重新转码：档位、编码（H.264 / HEVC / AV1 并存）与烧录水印
func (c *Client) TranscodeWith(ctx context.Context, id int64, opts *TranscodeOptions) (*Video, error) {
	var v Video
	body := map[string]any{}
	if opts != nil {
		if len(opts.Profiles) > 0 {
			body["profiles"] = opts.Profiles
		}
		if len(opts.Codecs) > 0 {
			body["codecs"] = opts.Codecs
		}
		switch {
		case opts.NoWatermark:
			body["watermark"] = map[string]any{}
		case opts.Watermark != nil:
			body["watermark"] = opts.Watermark
		}
	}
	if err := c.call(ctx, &request{method: http.MethodPost, path: videoPath(id, "/transcode"), json: jsonBody(body)}, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// CancelTranscode 取消进行中的转码（已有的可播放产物保留）
func (c *Client) CancelTranscode(ctx context.Context, id int64) (*Video, error) {
	var v Video
	if err := c.call(ctx, &request{method: http.MethodPost, path: videoPath(id, "/cancel")}, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteVideo 删除转码产物（保留源文件，播放回退为原文件）
func (c *Client) DeleteVideo(ctx context.Context, id int64) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: videoPath(id, ""), idempotent: true}, nil)
}

// WaitVideo 轮询直到转码结束（完成、失败或取消）或 ctx 结束；interval 为 0 时每 2 秒查询一次
func (c *Client) WaitVideo(ctx context.Context, id int64, interval time.Duration) (*Video, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		v, err := c.Video(ctx, id)
		if err != nil || v.Done() {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Play 签发播放地址：有可播放产物时 URL 为 HLS 主清单，否则 FallbackURL 为原文件的统一入口链接
func (c *Client) Play(ctx context.Context, id int64, opts *PlayOptions) (*Play, error) {
	q := url.Values{}
	if opts != nil {
		if n := expiresIn(opts.Expires); n > 0 {
			q.Set("expires", strconv.FormatInt(n, 10))
		}
		if opts.MaxVisits > 0 {
			q.Set("max_visits", strconv.FormatInt(opts.MaxVisits, 10))
		}
		if opts.Revocable {
			q.Set("revocable", "true")
		}
		if opts.Password != "" {
			q.Set("password", opts.Password)
		}
	}
	var p Play
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/play"), query: q, idempotent: true}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Subtitle 视频字幕轨
type Subtitle struct {
	// Lang BCP 47 语言标签（服务端规范化大小写，如 zh-CN）
	Lang      string    `json:"lang"`
	Label     string    `json:"label"`
	Default   bool      `json:"default"`
	Bytes     int       `json:"bytes"`
	UpdatedAt time.Time `json:"updated_at"`
	// Content WebVTT 内容，仅 Subtitle 返回
	Content string `json:"content,omitempty"`
}

// SubtitleOptions 上传字幕的选项（可为 nil）
type SubtitleOptions struct {
	// Label 播放器中显示的名称（如“简体中文”），空时用语言标签
	Label string
	// Default 设为默认字幕（取消其它字幕的默认）
	Default bool
}

func subtitlePath(id int64, lang string) string {
	p := filePath(id, "/subtitles")
	if lang != "" {
		p += "/" + url.PathEscape(lang)
	}
	return p
}

// Subtitles 视频文件的字幕轨（不含内容），默认字幕在前
func (c *Client) Subtitles(ctx context.Context, id int64) ([]Subtitle, error) {
	var list []Subtitle
	if err := c.call(ctx, &request{method: http.MethodGet, path: subtitlePath(id, ""), idempotent: true}, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Subtitle 某一语言的字幕（含 WebVTT 内容）
func (c *Client) Subtitle(ctx context.Context, id int64, lang string) (*Subtitle, error) {
	var s Subtitle
	if err := c.call(ctx, &request{method: http.MethodGet, path: subtitlePath(id, lang), idempotent: true}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// PutSubtitle 上传或替换某一语言的字幕：content 为 WebVTT 或 SRT（UTF-8，服务端转换为 WebVTT，不超过 512 KB），每个文件最多 20 条
func (c *Client) PutSubtitle(ctx context.Context, id int64, lang, content string, opts *SubtitleOptions) (*Subtitle, error) {
	body := map[string]any{"content": content}
	if opts != nil {
		body["label"], body["default"] = opts.Label, opts.Default
	}
	var s Subtitle
	if err := c.call(ctx, &request{method: http.MethodPut, path: subtitlePath(id, lang), json: jsonBody(body), idempotent: true}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSubtitle 删除某一语言的字幕
func (c *Client) DeleteSubtitle(ctx context.Context, id int64, lang string) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: subtitlePath(id, lang), idempotent: true}, nil)
}
