package fileapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// HeaderUploadToken 前端凭上传令牌上传时携带令牌的请求头
const HeaderUploadToken = "X-Upload-Token"

const (
	uploadTokenSigningPrefix = "fileapi-upload-token/v1\n"
	defaultUploadTokenTTL    = 15 * time.Minute
	maxUploadTokenTTL        = 7 * 24 * time.Hour
)

// UploadTokenOptions 前端上传令牌的限制，零值字段表示沿用客户端自身的限制
type UploadTokenOptions struct {
	// ExpiresIn 有效期，默认 15 分钟，最长 7 天
	ExpiresIn time.Duration
	// MaxSize 单个文件的大小上限（字节），不超过客户端自身的上限
	MaxSize int64
	// ContentTypes 允许的文件类型（服务端按内容识别），如 "image/*"、"application/pdf"
	ContentTypes []string
	// Extensions 允许的扩展名，如 "jpg"，与客户端的扩展名白名单同时生效
	Extensions []string
	// ObjectType / ObjectID / Metadata / ReplaceExisting 上传的文件关联的业务对象与元数据，上传方不能修改
	ObjectType      string
	ObjectID        string
	Metadata        map[string]string
	ReplaceExisting bool
	// FileExpiresIn 上传的文件多久后过期（按秒向上取整），0 表示不过期
	FileExpiresIn time.Duration
	// MaxUploads 可上传的文件数，默认 1
	MaxUploads int
	// VersionOf 凭令牌上传的文件作为该文件的新版本（文件 ID 与链接不变，旧内容保留为历史版本，见 UploadOptions.VersionOf）；
	// 不能与 ObjectType、ObjectID、Metadata、ReplaceExisting、FileExpiresIn 同时使用。文件不存在时上传返回 IsNotFound 错误
	VersionOf int64
	// Folder 上传的文件所在目录（如 "/头像"），上传方不能修改；不能与 VersionOf 同时使用
	Folder string
	// TagIDs 上传的文件的标签（本客户端的标签 ID，最多 20 个），上传方不能修改；不能与 VersionOf 同时使用
	TagIDs []int64
	// Transcode / Profiles 凭令牌上传的视频转码为多清晰度 HLS，上传方不能修改
	Transcode bool
	Profiles  []string
}

// UploadToken 前端上传令牌
type UploadToken struct {
	Token     string
	Nonce     string
	ExpiresAt time.Time
}

// uploadTokenClaims 令牌内容，字段与服务端一致
type uploadTokenClaims struct {
	ClientID        string            `json:"client_id"`
	Nonce           string            `json:"nonce"`
	ExpiresAt       int64             `json:"expires_at"`
	MaxSize         int64             `json:"max_size,omitempty"`
	ContentTypes    []string          `json:"content_types,omitempty"`
	Extensions      []string          `json:"extensions,omitempty"`
	ObjectType      string            `json:"object_type,omitempty"`
	ObjectID        string            `json:"object_id,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ReplaceExisting bool              `json:"replace_existing,omitempty"`
	FileExpiresIn   int64             `json:"file_expires_in,omitempty"`
	MaxUploads      int               `json:"max_uploads,omitempty"`
	VersionOf       int64             `json:"version_of,omitempty"`
	Folder          string            `json:"folder,omitempty"`
	TagIDs          []int64           `json:"tag_ids,omitempty"`
	Transcode       bool              `json:"transcode,omitempty"`
	Profiles        []string          `json:"profiles,omitempty"`
}

// NewUploadToken 用客户端密钥在本地签发前端上传令牌（不请求 FileApi）。把令牌交给浏览器 / 小程序，
// 它们在请求头 X-Upload-Token 中携带令牌调用 POST {baseURL}/upload/v1/files 直接上传，不需要持有客户端密钥，
// 文件也不经业务后端中转。上传完成后照常触发 file.uploaded Webhook。
func (c *Client) NewUploadToken(opts *UploadTokenOptions) (*UploadToken, error) {
	if opts == nil {
		opts = &UploadTokenOptions{}
	}
	ttl := opts.ExpiresIn
	if ttl <= 0 {
		ttl = defaultUploadTokenTTL
	}
	if ttl > maxUploadTokenTTL {
		return nil, errors.New("fileapi: upload token lifetime exceeds 7 days")
	}
	if opts.VersionOf > 0 && (opts.ObjectType != "" || opts.ObjectID != "" || len(opts.Metadata) > 0 || opts.ReplaceExisting || opts.FileExpiresIn > 0 || opts.Folder != "" || len(opts.TagIDs) > 0) {
		return nil, errors.New("fileapi: VersionOf cannot be combined with ObjectType, ObjectID, Metadata, ReplaceExisting, FileExpiresIn, Folder or TagIDs")
	}
	if len(opts.TagIDs) > 20 {
		return nil, errors.New("fileapi: at most 20 TagIDs")
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	expiresAt := c.now().Add(ttl).Truncate(time.Second)
	claims := uploadTokenClaims{
		ClientID: c.clientID, Nonce: hex.EncodeToString(buf[:]), ExpiresAt: expiresAt.Unix(), MaxSize: opts.MaxSize,
		ContentTypes: opts.ContentTypes, Extensions: opts.Extensions, ObjectType: opts.ObjectType, ObjectID: opts.ObjectID,
		Metadata: opts.Metadata, ReplaceExisting: opts.ReplaceExisting, FileExpiresIn: int64(math.Ceil(opts.FileExpiresIn.Seconds())),
		MaxUploads: opts.MaxUploads, VersionOf: opts.VersionOf, Folder: opts.Folder, TagIDs: opts.TagIDs,
		Transcode: opts.Transcode || len(opts.Profiles) > 0, Profiles: opts.Profiles,
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return nil, err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return &UploadToken{Token: payload + "." + signUploadToken(c.secret, payload), Nonce: claims.Nonce, ExpiresAt: expiresAt}, nil
}

// signUploadToken base64url(HMAC-SHA256(secret, "fileapi-upload-token/v1\n" + payload))
func signUploadToken(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(uploadTokenSigningPrefix))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
