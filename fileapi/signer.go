package fileapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// 签名算法与服务端一致：
//
//	StringToSign = Method + "\n" + Path + "\n" + CanonicalQuery + "\n"
//	             + ClientId + "\n" + Timestamp + "\n" + Nonce + "\n" + ContentSHA256
//	Signature    = Base64(HMAC-SHA256(ClientSecret, StringToSign))
//
// Path 是服务端看到的路径（/open/v1/...，不含反向代理前缀与查询串）；CanonicalQuery 按参数名、参数值升序，
// 名与值按 RFC 3986 编码（空格为 %20）后以 & 连接；ContentSHA256 为请求体 SHA-256 小写十六进制，
// multipart/form-data 上传使用 UNSIGNED-PAYLOAD。

const (
	headerClientID      = "X-Client-Id"
	headerTimestamp     = "X-Timestamp"
	headerNonce         = "X-Nonce"
	headerContentSHA256 = "X-Content-SHA256"
	headerSignature     = "X-Signature"

	unsignedPayload = "UNSIGNED-PAYLOAD"
)

var emptySHA256 = hashHex(nil)

func stringToSign(method, path string, query url.Values, clientID, timestamp, nonce, contentSHA256 string) string {
	return strings.Join([]string{strings.ToUpper(method), path, canonicalQuery(query), clientID, timestamp, nonce, contentSHA256}, "\n")
}

func sign(secret, sts string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sts))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			if sb.Len() > 0 {
				sb.WriteByte('&')
			}
			sb.WriteString(escape(k))
			sb.WriteByte('=')
			sb.WriteString(escape(v))
		}
	}
	return sb.String()
}

func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
