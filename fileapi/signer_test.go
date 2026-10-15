package fileapi

import (
	"net/url"
	"testing"
)

// 与服务端 pkg/signer、管理后台前端共用的签名向量，保证三端算法一致
func TestSignatureVector(t *testing.T) {
	q := url.Values{"page": {"1"}, "original_filename": {"a b*(中)"}, "tag": {"z", "a"}}
	if got, want := canonicalQuery(q), "original_filename=a%20b%2A%28%E4%B8%AD%29&page=1&tag=a&tag=z"; got != want {
		t.Fatalf("canonical query = %q, want %q", got, want)
	}
	sts := stringToSign("get", "/open/v1/files", q, "fa_test", "1700000000", "0123456789abcdef0123456789abcdef", emptySHA256)
	if got, want := sign("s3cr3t", sts), "mF15TjeOULRWcLkn6c1TWEuzl0zPb48HyFkfiF2cx28="; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if canonicalQuery(nil) != "" {
		t.Fatal("empty query must canonicalize to empty string")
	}
}
