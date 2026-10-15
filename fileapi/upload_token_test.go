package fileapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 与服务端 pkg/uploadtoken、Java / Node.js / Python SDK 共用的测试向量
func TestUploadTokenVector(t *testing.T) {
	payload := "eyJjbGllbnRfaWQiOiJmYV90ZXN0Iiwibm9uY2UiOiIwMTIzNDU2Nzg5YWJjZGVmIiwiZXhwaXJlc19hdCI6MTcwMDAwMDAwMCwibWF4X3NpemUiOjEwNDg1NzYsImNvbnRlbnRfdHlwZXMiOlsiaW1hZ2UvKiJdfQ"
	if got := signUploadToken("s3cr3t", payload); got != "KyOrpNCjE0WLGzetVTEapAUQt944wsNyKAU-PH2kefw" {
		t.Fatalf("signature = %s", got)
	}
}

func TestNewUploadToken(t *testing.T) {
	c, err := New("http://files.test", "fa_test", "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.NewUploadToken(&UploadTokenOptions{ExpiresIn: time.Hour, MaxSize: 1 << 20, ContentTypes: []string{"image/*"},
		ObjectType: "avatar", ObjectID: "42", Metadata: map[string]string{"k": "v"}, FileExpiresIn: 1500 * time.Millisecond, MaxUploads: 3})
	if err != nil {
		t.Fatal(err)
	}
	payload, sig, ok := strings.Cut(tok.Token, ".")
	if !ok || sig != signUploadToken("s3cr3t", payload) {
		t.Fatalf("token = %s", tok.Token)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["client_id"] != "fa_test" || claims["nonce"] != tok.Nonce || len(tok.Nonce) != 32 || claims["object_id"] != "42" ||
		claims["file_expires_in"].(float64) != 2 || claims["max_uploads"].(float64) != 3 || int64(claims["expires_at"].(float64)) != tok.ExpiresAt.Unix() {
		t.Fatalf("claims = %v", claims)
	}
	if time.Until(tok.ExpiresAt) < 59*time.Minute {
		t.Fatalf("expires at %v", tok.ExpiresAt)
	}
	if _, ok := claims["replace_existing"]; ok {
		t.Fatal("zero fields should be omitted")
	}
	if _, err := c.NewUploadToken(&UploadTokenOptions{ExpiresIn: 8 * 24 * time.Hour}); err == nil {
		t.Fatal("too long lifetime accepted")
	}
	if tok, err := c.NewUploadToken(nil); err != nil || time.Until(tok.ExpiresAt) > 16*time.Minute {
		t.Fatalf("default token = %+v, %v", tok, err)
	}
	// 上传新版本的令牌
	vtok, err := c.NewUploadToken(&UploadTokenOptions{VersionOf: 7})
	if err != nil {
		t.Fatal(err)
	}
	vpayload, _, _ := strings.Cut(vtok.Token, ".")
	raw, _ = base64.RawURLEncoding.DecodeString(vpayload)
	claims = nil
	if err := json.Unmarshal(raw, &claims); err != nil || claims["version_of"] != float64(7) {
		t.Fatalf("version claims = %v, %v", claims, err)
	}
	if _, err := c.NewUploadToken(&UploadTokenOptions{VersionOf: 7, ObjectType: "x", ObjectID: "1"}); err == nil {
		t.Fatal("version_of with object accepted")
	}
}
