//go:build integration

package fileapi_test

// 对运行中的 FileApi 做端到端测试：用管理接口创建临时客户端，走完 SDK 全部能力后清理。
//
//	FILEAPI_ADMIN_API_KEY=... go test -tags=integration ./...
//
// 环境变量：FILEAPI_BASE_URL（默认 http://127.0.0.1:8080）、FILEAPI_ADMIN_API_KEY（必填）、
// FILEAPI_TEST_STORAGE_ID（可选，客户端绑定的存储，默认使用默认存储）。
// 直传与统一入口链接需要测试进程能访问存储平台的 Endpoint。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apersonw/fileapi-go/fileapi"
)

type admin struct {
	t    *testing.T
	base string
	key  string
}

func (a *admin) do(method, path string, body, out any) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, r)
	req.Header.Set("X-Admin-Key", a.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.Code != 0 {
		a.t.Fatalf("admin %s %s: code=%d %s %v", method, path, env.Code, env.Message, err)
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			a.t.Fatal(err)
		}
	}
}

// newTestClient 创建临时客户端，测试结束时删除其全部文件与客户端本身
func newTestClient(t *testing.T) (*fileapi.Client, string, string) {
	base := strings.TrimRight(os.Getenv("FILEAPI_BASE_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	key := os.Getenv("FILEAPI_ADMIN_API_KEY")
	if key == "" {
		t.Skip("FILEAPI_ADMIN_API_KEY not set")
	}
	a := &admin{t: t, base: base, key: key}
	body := map[string]any{"client_name": "sdk-go integration", "remark": "sdk/go integration_test.go"}
	if id := os.Getenv("FILEAPI_TEST_STORAGE_ID"); id != "" {
		n, _ := strconv.ParseInt(id, 10, 64)
		body["storage_setting_id"] = n
	}
	var created struct {
		ID           int64  `json:"id"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	a.do(http.MethodPost, "/admin/v1/clients", body, &created)
	t.Cleanup(func() {
		// 解除保留锁定（管理端才能清除保留期限），彻底删除客户端的全部文件（含回收站中的），再删除客户端
		var locked struct {
			List []struct {
				ID int64 `json:"id"`
			} `json:"list"`
		}
		a.do(http.MethodGet, "/admin/v1/files?page_size=100&locked=true&client_id="+created.ClientID, nil, &locked)
		for _, f := range locked.List {
			a.do(http.MethodPut, fmt.Sprintf("/admin/v1/files/%d/retention", f.ID), map[string]any{"clear_retention": true, "legal_hold": false}, nil)
		}
		for _, filter := range []string{"", "&trashed=true"} {
			for {
				var page struct {
					List []struct {
						ID int64 `json:"id"`
					} `json:"list"`
				}
				a.do(http.MethodGet, "/admin/v1/files?page_size=100&client_id="+created.ClientID+filter, nil, &page)
				if len(page.List) == 0 {
					break
				}
				ids := make([]int64, len(page.List))
				for i, f := range page.List {
					ids[i] = f.ID
				}
				a.do(http.MethodPost, "/admin/v1/files/batch-delete", map[string]any{"ids": ids, "permanent": true}, nil)
			}
		}
		a.do(http.MethodDelete, fmt.Sprintf("/admin/v1/clients/%d", created.ID), nil, nil)
	})
	c, err := fileapi.New(base, created.ClientID, created.ClientSecret)
	if err != nil {
		t.Fatal(err)
	}
	return c, base, created.ClientID
}

func randomBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func download(t *testing.T, c *fileapi.Client, id int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := c.Download(context.Background(), id, &buf); err != nil {
		t.Fatalf("download %d: %v", id, err)
	}
	return buf.Bytes()
}

func TestIntegrationSDK(t *testing.T) {
	c, base, clientID := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	content := randomBytes(t, 300<<10)

	// 代理上传：服务端校验 SDK 附带的 SHA-256
	up, err := c.Upload(ctx, "季度 报告.bin", bytes.NewReader(content), int64(len(content)), &fileapi.UploadOptions{
		ObjectType: "order", ObjectID: "sdk-1", Metadata: map[string]string{"source": "sdk-go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Reused || up.SHA256 != digest(content) || up.Size != int64(len(content)) || up.OriginalFilename != "季度 报告.bin" || up.Metadata["source"] != "sdk-go" {
		t.Fatalf("upload result %+v", up)
	}
	again, err := c.Upload(ctx, "other.bin", bytes.NewReader([]byte("x")), 1, &fileapi.UploadOptions{ObjectType: "order", ObjectID: "sdk-1"})
	if err != nil || !again.Reused || again.ID != up.ID {
		t.Fatalf("same object should reuse: %+v %v", again, err)
	}
	// 长度未知（分块编码）
	chunked, err := c.Upload(ctx, "chunked.txt", io.MultiReader(strings.NewReader("hello "), strings.NewReader("chunked")), -1, nil)
	if err != nil || chunked.SHA256 != digest([]byte("hello chunked")) {
		t.Fatalf("chunked upload: %+v %v", chunked, err)
	}
	path := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(path, []byte("from disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := c.UploadFile(ctx, path, nil)
	if err != nil || local.OriginalFilename != "local.txt" || local.Category != fileapi.CategoryDocument {
		t.Fatalf("upload file: %+v %v", local, err)
	}

	// 查询与下载
	got, err := c.Get(ctx, up.ID)
	if err != nil || got.SHA256 != up.SHA256 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if b := download(t, c, up.ID); !bytes.Equal(b, content) {
		t.Fatal("downloaded content differs")
	}
	// 用量：刚上传与下载的内容已计入本月流量
	if u, err := c.Usage(ctx); err != nil || u.Files < 2 || u.Month == "" ||
		u.Traffic.ProxyBytes < int64(len(content)) || u.Traffic.UploadBytes < int64(len(content)) {
		t.Fatalf("usage: %+v %v", u, err)
	}
	obj, err := c.OpenRange(ctx, up.ID, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(obj)
	obj.Close()
	if !bytes.Equal(part, content[1000:1100]) || obj.Size != int64(len(content)) || obj.Filename != "季度 报告.bin" {
		t.Fatalf("range: %d bytes, size %d, filename %q", len(part), obj.Size, obj.Filename)
	}
	renamed, err := c.Rename(ctx, up.ID, "renamed.bin")
	if err != nil || renamed.OriginalFilename != "renamed.bin" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	// 过期时间：设置后返回，传零值取消
	at := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	if exp, err := c.SetExpiration(ctx, up.ID, at); err != nil || exp.ExpiresAt == nil || !exp.ExpiresAt.Equal(at) {
		t.Fatalf("set expiration: %+v %v", exp, err)
	}
	if exp, err := c.SetExpiration(ctx, up.ID, time.Time{}); err != nil || exp.ExpiresAt != nil {
		t.Fatalf("clear expiration: %+v %v", exp, err)
	}

	// 访问链接：统一入口（跟随 302 到存储）与存储直链
	for _, mode := range []string{fileapi.ModeGateway, fileapi.ModeDirect} {
		link, err := c.AccessURL(ctx, up.ID, &fileapi.URLOptions{Mode: mode, Expires: 10 * time.Minute})
		if err != nil || link.ExpiresAt == nil {
			t.Fatalf("%s url: %+v %v", mode, link, err)
		}
		if mode == fileapi.ModeGateway && !strings.HasPrefix(link.URL, base+"/a/") {
			t.Fatalf("gateway url %q", link.URL)
		}
		resp, err := http.Get(link.URL)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Equal(b, content) {
			t.Fatalf("%s link: http %d, %d bytes", mode, resp.StatusCode, len(b))
		}
	}

	// 按需图片处理：服务端缩放后返回；不是图片时拒绝签发
	if _, err := c.AccessURL(ctx, up.ID, &fileapi.URLOptions{Image: &fileapi.ImageOptions{Width: 100}}); !fileapi.IsCode(err, fileapi.CodeBadRequest) {
		t.Fatalf("image options on a non-image: %v", err)
	}
	pic := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for i := range pic.Pix {
		pic.Pix[i] = 0xff
	}
	var picBuf bytes.Buffer
	if err := png.Encode(&picBuf, pic); err != nil {
		t.Fatal(err)
	}
	picFile, err := c.Upload(ctx, "pic.png", bytes.NewReader(picBuf.Bytes()), int64(picBuf.Len()), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 媒体信息：生成缩略图的图片上传时即返回宽高；可按宽高筛选
	if picFile.Width == nil || *picFile.Width != 120 || *picFile.Height != 80 {
		t.Fatalf("image size: %v x %v", picFile.Width, picFile.Height)
	}
	if page, err := c.List(ctx, &fileapi.ListQuery{MinWidth: 120, MaxHeight: 80}); err != nil || len(page.List) != 1 || page.List[0].ID != picFile.ID {
		t.Fatalf("list by size: %+v %v", page, err)
	}
	link, err := c.AccessURL(ctx, picFile.ID, &fileapi.URLOptions{Image: &fileapi.ImageOptions{Width: 60}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(resp.Body)
	resp.Body.Close()
	if err != nil || format != "jpeg" || cfg.Width != 60 || cfg.Height != 40 {
		t.Fatalf("processed image: %s %+v %v", format, cfg, err)
	}
	// 水印：白底上居中的黑字
	link, err = c.AccessURL(ctx, picFile.ID, &fileapi.URLOptions{Image: &fileapi.ImageOptions{Width: 60, Format: fileapi.FormatPNG,
		Watermark: &fileapi.Watermark{Text: "SDK", Color: "000000", Position: fileapi.PositionCenter, Size: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err = http.Get(link.URL); err != nil {
		t.Fatal(err)
	}
	marked, err := png.Decode(resp.Body)
	resp.Body.Close()
	if err != nil || marked.Bounds().Dx() != 60 {
		t.Fatalf("watermarked image: %v", err)
	}
	dark := 0
	for y := 0; y < marked.Bounds().Dy(); y++ {
		for x := 0; x < marked.Bounds().Dx(); x++ {
			if r, _, _, _ := marked.At(x, y).RGBA(); r < 0x8000 {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Fatal("watermark text not drawn")
	}
	if err := c.Delete(ctx, picFile.ID); err != nil {
		t.Fatal(err)
	}

	// 直传：单次 PUT 与分片并发（11MiB，5MiB 分片 → 3 片）
	small := randomBytes(t, 64<<10)
	direct, err := c.UploadDirect(ctx, "direct.bin", bytes.NewReader(small), int64(len(small)), nil)
	if err != nil || direct.Status != fileapi.FileStatusActive {
		t.Fatalf("direct upload: %+v %v", direct, err)
	}
	if b := download(t, c, direct.ID); !bytes.Equal(b, small) {
		t.Fatal("direct upload content differs")
	}
	big := randomBytes(t, 11<<20)
	var last int64
	multi, err := c.UploadDirect(ctx, "multipart.bin", bytes.NewReader(big), int64(len(big)), &fileapi.DirectUploadOptions{
		MultipartThreshold: 1 << 20, PartSize: 5 << 20, Concurrency: 2,
		Progress: func(sent, total int64) { last = sent },
	})
	if err != nil || multi.Size != int64(len(big)) || last != int64(len(big)) {
		t.Fatalf("multipart upload: %+v %v progress=%d", multi, err, last)
	}
	if b := download(t, c, multi.ID); digest(b) != digest(big) {
		t.Fatal("multipart content differs")
	}

	// 断点续传：读到第 2 个分片时出错，保留上传；之后从中断处继续
	_, err = c.UploadDirect(ctx, "resume.bin", failingReaderAt{r: bytes.NewReader(big), failFrom: 5 << 20}, int64(len(big)), &fileapi.DirectUploadOptions{
		MultipartThreshold: 1 << 20, PartSize: 5 << 20, Concurrency: 1, Resumable: true,
	})
	var re *fileapi.ResumableError
	if !errors.As(err, &re) {
		t.Fatalf("expected resumable error, got %v", err)
	}
	st, err := c.MultipartStatus(ctx, re.FileID)
	if err != nil || st.PartCount != 3 || len(st.Uploaded) != 1 || st.Uploaded[0].PartNumber != 1 || st.UploadedBytes != 5<<20 {
		t.Fatalf("multipart status: %+v %v", st, err)
	}
	resumed, err := c.ResumeUpload(ctx, re.FileID, bytes.NewReader(big), int64(len(big)), nil)
	if err != nil || resumed.Status != fileapi.FileStatusActive {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	if b := download(t, c, resumed.ID); digest(b) != digest(big) {
		t.Fatal("resumed content differs")
	}

	// 列表：游标遍历（每页 2 条）与过滤
	var ids []int64
	for f, err := range c.ListAll(ctx, &fileapi.ListQuery{PageSize: 2, Sort: fileapi.SortSize, Order: fileapi.OrderAsc}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	if len(ids) != 6 { // 代理上传 3 个（复用的不算）+ 直传 2 个 + 续传 1 个
		t.Fatalf("ListAll returned %d files: %v", len(ids), ids)
	}
	page, err := c.List(ctx, &fileapi.ListQuery{OriginalFilename: "renamed", WithTotal: true})
	if err != nil || page.Total == nil || *page.Total != 1 || page.List[0].ID != up.ID {
		t.Fatalf("filtered list: %+v %v", page, err)
	}
	batch, err := c.BatchGet(ctx, ids)
	if err != nil || len(batch) != len(ids) {
		t.Fatalf("batch get: %d %v", len(batch), err)
	}

	// 秒传（服务端默认开启去重）：相同内容命中，不传输内容；删除原文件后副本仍可下载（对象按引用计数删除）
	inst, err := c.UploadInstant(ctx, "副本.bin", digest(content), int64(len(content)), nil)
	if err != nil || !inst.Hit || inst.File.SHA256 != up.SHA256 || inst.File.OriginalFilename != "副本.bin" {
		t.Fatalf("instant: %+v %v", inst, err)
	}
	tried, err := c.Upload(ctx, "tried.bin", bytes.NewReader(content), int64(len(content)), &fileapi.UploadOptions{TryInstant: true})
	if err != nil || !tried.Instant || tried.SHA256 != up.SHA256 {
		t.Fatalf("try instant: %+v %v", tried, err)
	}
	fresh := randomBytes(t, 4<<10)
	missed, err := c.Upload(ctx, "fresh.bin", bytes.NewReader(fresh), int64(len(fresh)), &fileapi.UploadOptions{TryInstant: true, ExpiresIn: time.Hour})
	if err != nil || missed.Instant || missed.Deduplicated || missed.SHA256 != digest(fresh) || missed.ExpiresAt == nil ||
		missed.ExpiresAt.Sub(time.Now().Add(time.Hour)).Abs() > time.Minute {
		t.Fatalf("try instant miss: %+v %v", missed, err)
	}
	dup, err := c.Upload(ctx, "dup.bin", bytes.NewReader(fresh), int64(len(fresh)), nil)
	if err != nil || !dup.Deduplicated {
		t.Fatalf("proxy dedup: %+v %v", dup, err)
	}

	// 元数据：按键值检索、合并修改与删除键
	if page, err := c.List(ctx, &fileapi.ListQuery{Metadata: map[string]string{"source": "sdk-go"}}); err != nil || len(page.List) != 1 || page.List[0].ID != up.ID {
		t.Fatalf("list by metadata: %+v %v", page, err)
	}
	if f, err := c.UpdateMetadata(ctx, up.ID, map[string]string{"stage": "final"}); err != nil || f.Metadata["stage"] != "final" || f.Metadata["source"] != "sdk-go" {
		t.Fatalf("update metadata: %+v %v", f, err)
	}
	if page, err := c.List(ctx, &fileapi.ListQuery{MetadataKeys: []string{"stage"}}); err != nil || len(page.List) != 1 {
		t.Fatalf("list by metadata key: %+v %v", page, err)
	}
	if f, err := c.UpdateMetadata(ctx, up.ID, nil, "stage"); err != nil || len(f.Metadata) != 1 {
		t.Fatalf("remove metadata key: %+v %v", f, err)
	}

	// 删除：服务端默认开启回收站，删除的文件进入回收站，可以恢复，也可以彻底删除
	if err := c.Delete(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, up.ID); !fileapi.IsNotFound(err) {
		t.Fatalf("deleted file: %v", err)
	}
	trash, err := c.ListTrash(ctx, &fileapi.ListQuery{ObjectType: "order", ObjectID: "sdk-1"})
	if err != nil || len(trash.List) != 1 || trash.List[0].ID != up.ID || trash.List[0].Status != fileapi.FileStatusTrashed || trash.List[0].PurgeAt == nil {
		t.Fatalf("list trash: %+v %v", trash, err)
	}
	if restored, err := c.Restore(ctx, up.ID); err != nil || restored.Status != fileapi.FileStatusActive {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if b := download(t, c, up.ID); !bytes.Equal(b, content) {
		t.Fatal("restored file content changed")
	}
	if err := c.DeletePermanently(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(ctx, up.ID); !fileapi.IsNotFound(err) {
		t.Fatalf("restore permanently deleted file: %v", err)
	}
	if b := download(t, c, inst.File.ID); !bytes.Equal(b, content) {
		t.Fatal("shared object removed while still referenced")
	}
	if res, err := c.BatchDelete(ctx, []int64{inst.File.ID, tried.ID, missed.ID, dup.ID}); err != nil || len(res.Deleted) != 4 || len(res.Trashed) != 4 {
		t.Fatalf("delete shared files: %+v %v", res, err)
	}
	if n, err := c.EmptyTrash(ctx); err != nil || n < 4 {
		t.Fatalf("empty trash: %d %v", n, err)
	}
	res, err := c.BatchDeletePermanently(ctx, []int64{direct.ID, multi.ID, up.ID})
	if err != nil || len(res.Deleted) != 2 || len(res.Trashed) != 0 || res.Failed[up.ID] == "" {
		t.Fatalf("batch delete: %+v %v", res, err)
	}

	// 批量打包下载：包内路径可自定义，下载得到 ZIP
	arch, err := c.CreateArchive(ctx, []fileapi.ArchiveFile{{ID: local.ID, Name: "文档/说明.txt"}, {ID: local.ID}}, &fileapi.ArchiveOptions{Filename: "pack"})
	if err != nil || arch.Files != 2 || arch.Filename != "pack.zip" {
		t.Fatalf("create archive: %+v %v", arch, err)
	}
	zresp, err := http.Get(arch.URL)
	if err != nil {
		t.Fatal(err)
	}
	zdata, _ := io.ReadAll(zresp.Body)
	zresp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(zdata), int64(len(zdata)))
	if err != nil || len(zr.File) != 2 || zr.File[0].Name != "文档/说明.txt" || zr.File[1].Name != "local.txt" {
		t.Fatalf("archive content: %v", err)
	}

	// 限次链接：访问 1 次后用完；可吊销链接吊销后立即失效
	limited, err := c.AccessURL(ctx, local.ID, &fileapi.URLOptions{Expires: time.Hour, MaxVisits: 1})
	if err != nil || limited.LinkID == "" || limited.MaxVisits != 1 {
		t.Fatalf("limited url: %+v %v", limited, err)
	}
	visit := func(u string) int {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if a, b := visit(limited.URL), visit(limited.URL); a != http.StatusOK || b != http.StatusGone {
		t.Fatalf("limited visits: %d %d", a, b)
	}
	if l, err := c.LinkStatus(ctx, limited.LinkID); err != nil || l.Visits != 1 || l.FileID != local.ID {
		t.Fatalf("link status: %+v %v", l, err)
	}
	revocable, err := c.AccessURL(ctx, local.ID, &fileapi.URLOptions{Expires: time.Hour, Revocable: true})
	if err != nil || revocable.LinkID == "" {
		t.Fatalf("revocable url: %+v %v", revocable, err)
	}
	if err := c.RevokeLink(ctx, revocable.LinkID); err != nil {
		t.Fatal(err)
	}
	if code := visit(revocable.URL); code != http.StatusGone {
		t.Fatalf("revoked visit: %d", code)
	}
	if _, err := c.LinkStatus(ctx, revocable.LinkID); !fileapi.IsNotFound(err) {
		t.Fatalf("revoked status: %v", err)
	}

	// 带提取码的链接：程序访问在请求头中携带提取码，缺少时返回 401
	protected, err := c.AccessURL(ctx, local.ID, &fileapi.URLOptions{Expires: time.Hour, Password: "Ab12"})
	if err != nil || !protected.PasswordProtected {
		t.Fatalf("password url: %+v %v", protected, err)
	}
	if code := visit(protected.URL); code != http.StatusUnauthorized {
		t.Fatalf("visit without password: %d", code)
	}
	preq, _ := http.NewRequest(http.MethodGet, protected.URL, nil)
	preq.Header.Set(fileapi.LinkPasswordHeader, "ab12")
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()
	if presp.StatusCode != http.StatusOK {
		t.Fatalf("visit with password: %d", presp.StatusCode)
	}

	// 访问统计：经统一入口访问后，热门文件与文件详情返回访问次数
	statLink, err := c.AccessURL(ctx, local.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		visit(statLink.URL)
	}
	hot, err := c.HotFiles(ctx, 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hot {
		if h.File.ID == local.ID && h.Visits >= 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("hot files missing local file: %+v", hot)
	}
	if got, err := c.Get(ctx, local.ID); err != nil || got.AccessCount < 2 || got.LastAccessedAt == nil {
		t.Fatalf("access count: %+v %v", got, err)
	}

	// 文件夹：上传到目录、列出子目录与文件、移动、重命名、打包整个目录、删除目录
	fa, err := c.Upload(ctx, "fa.txt", strings.NewReader("folder a"), 8, &fileapi.UploadOptions{Folder: "sdk/合同/2026"})
	if err != nil || fa.Folder != "/sdk/合同/2026/" {
		t.Fatalf("upload to folder: %+v %v", fa, err)
	}
	fb, err := c.UploadDirect(ctx, "fb.txt", strings.NewReader("folder b"), 8, &fileapi.DirectUploadOptions{Folder: "/sdk/合同"})
	if err != nil || fb.Folder != "/sdk/合同/" {
		t.Fatalf("direct to folder: %+v %v", fb, err)
	}
	if fl, err := c.ListFolders(ctx, "/sdk"); err != nil || fl.Path != "/sdk/" || len(fl.Folders) != 1 || fl.Folders[0].Name != "合同" || fl.Folders[0].Files != 2 {
		t.Fatalf("list folders: %+v %v", fl, err)
	}
	if page, err := c.List(ctx, &fileapi.ListQuery{Folder: "/sdk", Recursive: true}); err != nil || len(page.List) != 2 {
		t.Fatalf("list recursive: %+v %v", page, err)
	}
	if n, err := c.MoveFiles(ctx, []int64{fb.ID}, "/sdk/合同/2026"); err != nil || n != 1 {
		t.Fatalf("move files: %d %v", n, err)
	}
	if moved, err := c.MoveFile(ctx, fa.ID, "/sdk/合同"); err != nil || moved.Folder != "/sdk/合同/" {
		t.Fatalf("move file: %+v %v", moved, err)
	}
	if n, err := c.RenameFolder(ctx, "/sdk/合同", "/sdk/法务"); err != nil || n != 2 {
		t.Fatalf("rename folder: %d %v", n, err)
	}
	if a, err := c.CreateArchive(ctx, nil, &fileapi.ArchiveOptions{Folder: "/sdk"}); err != nil || a.Files != 2 {
		t.Fatalf("archive folder: %+v %v", a, err)
	}
	if res, err := c.DeleteFolder(ctx, "/sdk", true); err != nil || res.Deleted != 2 || res.Remaining != 0 {
		t.Fatalf("delete folder: %+v %v", res, err)
	}

	// 文件版本历史：上传新版本后文件 ID 不变，旧内容可以列出、下载、恢复
	doc, err := c.Upload(ctx, "doc.txt", strings.NewReader("doc v1"), 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := c.Upload(ctx, "doc-v2.txt", strings.NewReader("doc v2!"), 7, &fileapi.UploadOptions{VersionOf: doc.ID})
	if err != nil || v2.ID != doc.ID || v2.Version != 2 || v2.OriginalFilename != "doc-v2.txt" {
		t.Fatalf("upload version: %+v %v", v2, err)
	}
	v3, err := c.UploadDirect(ctx, "doc-v3.txt", strings.NewReader("doc v3 direct"), 13, &fileapi.DirectUploadOptions{VersionOf: doc.ID})
	if err != nil || v3.ID != doc.ID || v3.Version != 3 {
		t.Fatalf("direct version: %+v %v", v3, err)
	}
	versions, err := c.ListVersions(ctx, doc.ID)
	if err != nil || len(versions) != 2 || versions[1].Version != 1 || versions[1].OriginalFilename != "doc.txt" {
		t.Fatalf("list versions: %+v %v", versions, err)
	}
	vobj, err := c.OpenVersion(ctx, doc.ID, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	vdata, _ := io.ReadAll(vobj)
	vobj.Close()
	if string(vdata) != "doc v1" {
		t.Fatalf("version content: %q", vdata)
	}
	if restored, err := c.RestoreVersion(ctx, doc.ID, versions[1].ID); err != nil || restored.Version != 4 || restored.OriginalFilename != "doc.txt" {
		t.Fatalf("restore version: %+v %v", restored, err)
	}
	if err := c.DeleteVersion(ctx, doc.ID, versions[0].ID); err != nil {
		t.Fatal(err)
	}
	if versions, err = c.ListVersions(ctx, doc.ID); err != nil || len(versions) != 1 || versions[0].Version != 3 {
		t.Fatalf("versions after delete: %+v %v", versions, err)
	}
	// 凭限定 VersionOf 的上传令牌上传新版本（前端直传场景）
	vtok, err := c.NewUploadToken(&fileapi.UploadTokenOptions{VersionOf: doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	vreq, _ := http.NewRequest(http.MethodPost, base+"/upload/v1/files", nil)
	vbody := &bytes.Buffer{}
	vmw := multipart.NewWriter(vbody)
	vpart, _ := vmw.CreateFormFile("file", "doc-token.txt")
	vpart.Write([]byte("doc via token"))
	vmw.Close()
	vreq.Body = io.NopCloser(vbody)
	vreq.Header.Set("Content-Type", vmw.FormDataContentType())
	vreq.Header.Set(fileapi.HeaderUploadToken, vtok.Token)
	vresp, err := http.DefaultClient.Do(vreq)
	if err != nil {
		t.Fatal(err)
	}
	var vres struct {
		Data fileapi.File `json:"data"`
	}
	json.NewDecoder(vresp.Body).Decode(&vres)
	vresp.Body.Close()
	if vres.Data.ID != doc.ID || vres.Data.Version != 5 || vres.Data.OriginalFilename != "doc-token.txt" {
		t.Fatalf("token version upload: %d %+v", vresp.StatusCode, vres.Data)
	}
	if err := c.DeletePermanently(ctx, doc.ID); err != nil {
		t.Fatal(err)
	}

	// 错误的密钥
	bad, _ := fileapi.New(base, clientID, "wrong-secret")
	if _, err := bad.Get(ctx, local.ID); !fileapi.IsCode(err, fileapi.CodeSignature) {
		t.Fatalf("wrong secret: %v", err)
	}
}

// failingReaderAt 读取 failFrom 之后的内容时出错，模拟上传中断
type failingReaderAt struct {
	r        io.ReaderAt
	failFrom int64
}

func (f failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.failFrom {
		return 0, errors.New("simulated interruption")
	}
	return f.r.ReadAt(p, off)
}

// 前端上传令牌：业务后端本地签发，浏览器 / 小程序凭令牌直接上传（这里用 net/http 模拟前端）
func TestIntegrationUploadToken(t *testing.T) {
	c, base, _ := newTestClient(t)
	ctx := context.Background()
	tok, err := c.NewUploadToken(&fileapi.UploadTokenOptions{ContentTypes: []string{"image/*"}, MaxSize: 1 << 20,
		ObjectType: "avatar", ObjectID: "sdk-go", Metadata: map[string]string{"via": "token"}})
	if err != nil {
		t.Fatal(err)
	}
	upload := func(name string, data []byte) (int, map[string]any) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(data)
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, base+"/upload/v1/files", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set(fileapi.HeaderUploadToken, tok.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var env struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		return env.Code, env.Data
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	// 类型不符被拒，不占用次数
	if code, _ := upload("a.txt", []byte("plain text")); code != 41501 {
		t.Fatalf("text upload code = %d", code)
	}
	code, data := upload("avatar.png", pic.Bytes())
	if code != 0 || data["object_id"] != "sdk-go" {
		t.Fatalf("upload = %d %v", code, data)
	}
	f, err := c.Get(ctx, int64(data["id"].(float64)))
	if err != nil || f.ObjectType != "avatar" || f.Metadata["via"] != "token" || len(f.Metadata) != 1 {
		t.Fatalf("uploaded file = %+v, %v", f, err)
	}
	// 默认只能上传一次
	if code, _ := upload("again.png", pic.Bytes()); code != 40304 {
		t.Fatalf("second upload code = %d", code)
	}
}

// TestIntegrationVideo 视频转码与播放：需要服务端运行 transcoder（FILEAPI_TEST_VIDEO=1 时执行）
func TestIntegrationVideo(t *testing.T) {
	if os.Getenv("FILEAPI_TEST_VIDEO") != "1" {
		t.Skip("set FILEAPI_TEST_VIDEO=1 with a running transcoder")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	c, _, _ := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440", "-t", "3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path).CombinedOutput(); err != nil {
		t.Fatalf("make video: %v %s", err, out)
	}
	up, err := c.UploadFile(ctx, path, &fileapi.UploadOptions{Transcode: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.WaitVideo(ctx, up.ID, 500*time.Millisecond)
	if err != nil || v.Status != fileapi.VideoReady || len(v.Renditions) != 2 || v.Renditions[0].Name != "360p" || v.Renditions[0].AverageBandwidth <= 0 ||
		v.Renditions[1].Codec != fileapi.CodecAAC || !v.Dash {
		t.Fatalf("video = %+v, %v", v, err)
	}
	p, err := c.Play(ctx, up.ID, &fileapi.PlayOptions{Expires: 10 * time.Minute})
	if err != nil || !p.Playable {
		t.Fatalf("play = %+v, %v", p, err)
	}
	resp, err := http.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "360p/index.m3u8") {
		t.Fatalf("master = %d %s", resp.StatusCode, body)
	}
	// DASH 清单（音视频分离的产物）
	resp, err = http.Get(p.DashURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/dash+xml" || !strings.Contains(string(body), `contentType="audio"`) {
		t.Fatalf("mpd = %d %s", resp.StatusCode, body)
	}
	// 拖动预览缩略图（服务端默认开启 video.thumbnails）
	if v.Thumbnails == nil || v.Thumbnails.Count != 3 || p.ThumbnailsURL == "" {
		t.Fatalf("thumbnails = %+v %q", v.Thumbnails, p.ThumbnailsURL)
	}
	resp, err = http.Get(p.ThumbnailsURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "WEBVTT") || strings.Count(string(body), "#xywh=") != 3 {
		t.Fatalf("thumbs.vtt = %d %s", resp.StatusCode, body)
	}
	// 字幕：上传 SRT 后主清单列出字幕分组
	if s, err := c.PutSubtitle(ctx, up.ID, "zh-cn", "1\n00:00:00,500 --> 00:00:01,500\n你好\n", &fileapi.SubtitleOptions{Default: true}); err != nil || s.Lang != "zh-CN" {
		t.Fatalf("put subtitle = %+v, %v", s, err)
	}
	resp, err = http.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `LANGUAGE="zh-CN"`) {
		t.Fatalf("master without subtitles: %s", body)
	}
	if s, err := c.Subtitle(ctx, up.ID, "zh-CN"); err != nil || !strings.HasPrefix(s.Content, "WEBVTT") {
		t.Fatalf("subtitle = %+v, %v", s, err)
	}
	if err := c.DeleteSubtitle(ctx, up.ID, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	// 重新转码：H.264 + HEVC，烧录文字水印
	if v, err := c.TranscodeWith(ctx, up.ID, &fileapi.TranscodeOptions{Profiles: []string{"360p"}, Codecs: []string{fileapi.CodecH264, fileapi.CodecHEVC},
		Watermark: &fileapi.VideoWatermark{Text: "FileApi SDK"}}); err != nil || v.Status != fileapi.VideoPending || v.Watermark == nil || v.Watermark.Position != "se" {
		t.Fatalf("retranscode = %+v, %v", v, err)
	}
	if v, err = c.WaitVideo(ctx, up.ID, 500*time.Millisecond); err != nil || v.Status != fileapi.VideoReady || len(v.Renditions) != 3 || v.Renditions[1].Name != "360p-hevc" {
		t.Fatalf("retranscode wait = %+v, %v", v, err)
	}
	if err := c.DeleteVideo(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Video(ctx, up.ID); !fileapi.IsNotFound(err) {
		t.Fatalf("video after delete: %v", err)
	}
	if p, err := c.Play(ctx, up.ID, nil); err != nil || p.Playable || p.FallbackURL == "" {
		t.Fatalf("fallback = %+v, %v", p, err)
	}
	_ = c.DeletePermanently(ctx, up.ID)
}

func TestIntegrationShare(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	content := []byte("share content " + strconv.FormatInt(time.Now().UnixNano(), 10))
	up, err := c.Upload(ctx, "分享.txt", bytes.NewReader(content), int64(len(content)), &fileapi.UploadOptions{Folder: "/分享/"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.ShareFiles(ctx, []int64{up.ID}, &fileapi.ShareOptions{Password: "Ab12", MaxDownloads: 1, ExpiresIn: time.Hour})
	if err != nil || !s.PasswordProtected {
		t.Fatalf("share = %+v, %v", s, err)
	}
	// 分享页需要提取码；信息接口与下载在请求头中携带提取码
	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("page status = %d", resp.StatusCode)
	}
	get := func(u string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("X-Link-Password", "ab12")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	if _, body := get(s.URL + "/info"); !strings.Contains(string(body), "分享.txt") {
		t.Fatalf("info = %s", body)
	}
	if resp, body := get(s.URL + "/f/" + strconv.FormatInt(up.ID, 10) + "?download=1"); resp.StatusCode != 200 || !bytes.Equal(body, content) {
		t.Fatalf("download = %d %q", resp.StatusCode, body)
	}
	if got, err := c.Share(ctx, s.Code); err != nil || got.Downloads != 1 || got.Views != 1 {
		t.Fatalf("share stats = %+v, %v", got, err)
	}
	folder, err := c.ShareFolder(ctx, "/分享/", false, nil)
	if err != nil || folder.Folder != "/分享/" {
		t.Fatalf("folder share = %+v, %v", folder, err)
	}
	if p, err := c.Shares(ctx, 1, 10); err != nil || p.Total != 2 {
		t.Fatalf("shares = %+v, %v", p, err)
	}
	for _, code := range []string{s.Code, folder.Code} {
		if err := c.DeleteShare(ctx, code); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Share(ctx, s.Code); !fileapi.IsNotFound(err) {
		t.Fatalf("deleted share: %v", err)
	}
	_ = c.DeletePermanently(ctx, up.ID)
}

// sampleDocx 最小的 Word 文档
func sampleDocx(text string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`},
	} {
		w, _ := zw.Create(p[0])
		_, _ = w.Write([]byte(p[1]))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// TestIntegrationPreview 需要服务端运行文档预览转换 worker（FILEAPI_TEST_PREVIEW=1）
func TestIntegrationPreview(t *testing.T) {
	if os.Getenv("FILEAPI_TEST_PREVIEW") != "1" {
		t.Skip("set FILEAPI_TEST_PREVIEW=1 with a running preview worker")
	}
	c, _, _ := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	doc := sampleDocx("FileApi Go SDK 预览")
	up, err := c.Upload(ctx, "报告.docx", bytes.NewReader(doc), int64(len(doc)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RequestPreview(ctx, up.ID); err != nil && !fileapi.IsCode(err, fileapi.CodeConflict) {
		t.Fatal(err)
	}
	p, err := c.WaitPreview(ctx, up.ID, 500*time.Millisecond)
	if err != nil || p.Status != fileapi.PreviewReady || !p.Available {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	u, err := c.AccessURL(ctx, up.ID, &fileapi.URLOptions{Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("not a pdf: %d %q", resp.StatusCode, body[:min(16, len(body))])
	}
	_ = c.DeletePermanently(ctx, up.ID)
}

func TestIntegrationRetention(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	data := []byte("retained record")
	up, err := c.Upload(ctx, "合规.txt", bytes.NewReader(data), int64(len(data)), nil)
	if err != nil || up.Locked {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	until := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	f, err := c.SetRetention(ctx, up.ID, fileapi.RetentionOptions{RetainUntil: &until})
	if err != nil || !f.Locked || f.RetainUntil == nil || !f.RetainUntil.Equal(until) {
		t.Fatalf("set retention = %+v, %v", f, err)
	}
	shorter := until.Add(-time.Hour)
	if _, err := c.SetRetention(ctx, up.ID, fileapi.RetentionOptions{RetainUntil: &shorter}); !fileapi.IsCode(err, fileapi.CodeConflict) {
		t.Fatalf("shorten = %v", err)
	}
	if err := c.Delete(ctx, up.ID); !fileapi.IsCode(err, fileapi.CodeFileLocked) {
		t.Fatalf("delete locked = %v", err)
	}
	locked := true
	page, err := c.List(ctx, &fileapi.ListQuery{Locked: &locked})
	if err != nil || len(page.List) != 1 || page.List[0].ID != up.ID {
		t.Fatalf("list locked = %+v, %v", page, err)
	}
	// 法律保留：客户端可以设置与解除
	hold := true
	if f, err = c.SetRetention(ctx, up.ID, fileapi.RetentionOptions{LegalHold: &hold}); err != nil || !f.LegalHold {
		t.Fatalf("legal hold = %+v, %v", f, err)
	}
	hold = false
	if f, err = c.SetRetention(ctx, up.ID, fileapi.RetentionOptions{LegalHold: &hold}); err != nil || f.LegalHold || !f.Locked {
		t.Fatalf("release legal hold = %+v, %v", f, err)
	}
}

func TestIntegrationZip(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("资料/说明.txt")
	_, _ = w.Write([]byte("FileApi Go SDK 压缩包预览"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	up, err := c.Upload(ctx, "资料.zip", bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := c.ZipEntries(ctx, up.ID)
	if err != nil || l.Total != 1 || l.Entries[0].Path != "资料/说明.txt" {
		t.Fatalf("entries = %+v, %v", l, err)
	}
	obj, err := c.OpenZipEntry(ctx, up.ID, l.Entries[0].Index)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(obj)
	obj.Close()
	if string(data) != "FileApi Go SDK 压缩包预览" {
		t.Fatalf("entry = %q", data)
	}
}

func TestIntegrationUsageReport(t *testing.T) {
	c, _, clientID := newTestClient(t)
	ctx := context.Background()
	r, err := c.UsageReport(ctx, nil)
	if err != nil || len(r.Rows) != 1 || r.Rows[0].ClientID != clientID || !r.Rows[0].Partial || r.From != r.To {
		t.Fatalf("report = %+v, %v", r, err)
	}
	csv, err := c.UsageReportCSV(ctx, &fileapi.UsageReportOptions{Month: r.From})
	if err != nil || !bytes.HasPrefix(csv, []byte("\xef\xbb\xbf月份")) || !bytes.Contains(csv, []byte(clientID)) {
		t.Fatalf("csv = %q, %v", csv, err)
	}
	if _, err := c.UsageReport(ctx, &fileapi.UsageReportOptions{Month: "2999-01"}); !fileapi.IsCode(err, fileapi.CodeBadRequest) {
		t.Fatalf("future month = %v", err)
	}
}

func TestIntegrationTags(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	contract, err := c.CreateTag(ctx, "合同", fileapi.TagBlue)
	if err != nil {
		t.Fatal(err)
	}
	urgent, err := c.CreateTag(ctx, "Urgent", "#ff0000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateTag(ctx, "urgent", ""); !fileapi.IsCode(err, fileapi.CodeConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	a, _ := c.Upload(ctx, "a.txt", bytes.NewReader([]byte("tag a")), 5, nil)
	b, _ := c.Upload(ctx, "b.txt", bytes.NewReader([]byte("tag b")), 5, nil)
	f, err := c.SetFileTags(ctx, a.ID, []int64{contract.ID, urgent.ID})
	if err != nil || len(f.Tags) != 2 || f.Tags[0].Name != "Urgent" {
		t.Fatalf("set = %+v, %v", f, err)
	}
	if n, err := c.BatchTag(ctx, []int64{a.ID, b.ID}, []int64{contract.ID}, nil); err != nil || n != 2 {
		t.Fatalf("batch = %d, %v", n, err)
	}
	all, err := c.List(ctx, &fileapi.ListQuery{TagIDs: []int64{contract.ID, urgent.ID}})
	if err != nil || len(all.List) != 1 || all.List[0].ID != a.ID {
		t.Fatalf("all = %+v, %v", all, err)
	}
	anyOf, _ := c.List(ctx, &fileapi.ListQuery{TagIDs: []int64{contract.ID, urgent.ID}, TagAny: true})
	if len(anyOf.List) != 2 {
		t.Fatalf("any = %+v", anyOf)
	}
	tags, _ := c.ListTags(ctx)
	if len(tags) != 2 {
		t.Fatalf("tags = %+v", tags)
	}
	if n, err := c.DeleteTag(ctx, contract.ID); err != nil || n != 2 {
		t.Fatalf("delete = %d, %v", n, err)
	}
}

func TestIntegrationExportFiles(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	for _, n := range []string{"b.txt", "a.txt"} {
		if _, err := c.Upload(ctx, n, bytes.NewReader([]byte("export "+n)), int64(len("export "+n)), &fileapi.UploadOptions{Folder: "/清单/"}); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	res, err := c.ExportFiles(ctx, &fileapi.ListQuery{Sort: "original_filename", Order: "asc"}, &buf)
	if err != nil || res.Rows != 2 || res.Truncated {
		t.Fatalf("export = %+v, %v", res, err)
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(buf.String(), "\xef\xbb\xbf")), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "id,original_filename,folder") || !strings.Contains(lines[1], ",a.txt,/清单/,") {
		t.Fatalf("csv = %q", buf.String())
	}
}

func TestIntegrationTagLinkage(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	tag, err := c.CreateTag(ctx, "归档", fileapi.TagGreen)
	if err != nil {
		t.Fatal(err)
	}
	up, err := c.Upload(ctx, "a.txt", bytes.NewReader([]byte("linked a")), 8, &fileapi.UploadOptions{TagIDs: []int64{tag.ID}})
	if err != nil || len(up.Tags) != 1 || up.Tags[0].Name != "归档" {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	f, err := c.UploadDirect(ctx, "b.txt", bytes.NewReader([]byte("linked b")), 8, &fileapi.DirectUploadOptions{TagIDs: []int64{tag.ID}})
	if err != nil || len(f.Tags) != 1 {
		t.Fatalf("direct = %+v, %v", f, err)
	}
	sh, err := c.ShareTags(ctx, []int64{tag.ID}, nil)
	if err != nil || len(sh.FileIDs) != 2 {
		t.Fatalf("share = %+v, %v", sh, err)
	}
}

func TestIntegrationCollection(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	col, err := c.CreateCollection(ctx, "作业", &fileapi.CollectionOptions{Extensions: []string{"txt"}, MaxFiles: 5, RequireName: true})
	if err != nil || col.Folder != "/文件收集/作业/" || !col.RequireName {
		t.Fatalf("create = %+v, %v", col, err)
	}
	// 外部人员在收集页提交（无需签名）
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "张三")
	fw, _ := w.CreateFormFile("file", "作业.txt")
	_, _ = fw.Write([]byte("homework"))
	_ = w.Close()
	resp, err := http.Post(col.URL+"/upload?format=json", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	files, err := c.CollectionFiles(ctx, col.Code, nil)
	if err != nil || len(files.List) != 1 || files.List[0].Metadata[fileapi.UploaderMetaKey] != "张三" {
		t.Fatalf("files = %+v, %v", files, err)
	}
	off := false
	if u, err := c.UpdateCollection(ctx, col.Code, fileapi.CollectionUpdate{Enabled: &off}); err != nil || u.Enabled || u.Files != 1 || u.Submissions != 1 {
		t.Fatalf("update = %+v, %v", u, err)
	}
	if err := c.DeleteCollection(ctx, col.Code); err != nil {
		t.Fatal(err)
	}
	_, err = c.Collection(ctx, col.Code)
	if !fileapi.IsCode(err, fileapi.CodeNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestIntegrationTagRule(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	tag, err := c.CreateTag(ctx, "合同", fileapi.TagBlue)
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Upload(ctx, "旧.pdf", bytes.NewReader([]byte("old pdf")), 7, &fileapi.UploadOptions{Folder: "/合同/"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := c.CreateTagRule(ctx, fileapi.TagRule{Name: "合同 PDF", TagIDs: []int64{tag.ID}, Extensions: []string{"pdf"}, Folder: "/合同/", Enabled: true})
	if err != nil || rule.Extensions[0] != ".pdf" {
		t.Fatalf("rule = %+v, %v", rule, err)
	}
	up, err := c.Upload(ctx, "新.pdf", bytes.NewReader([]byte("new pdf")), 7, &fileapi.UploadOptions{Folder: "/合同/"})
	if err != nil || len(up.Tags) != 1 || up.Tags[0].ID != tag.ID {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	if p, err := c.PreviewTagRule(ctx, *rule); err != nil || p.Files != 1 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if a, err := c.ApplyTagRule(ctx, rule.ID); err != nil || a.Updated != 1 {
		t.Fatalf("apply = %+v, %v", a, err)
	}
	if f, err := c.Get(ctx, old.ID); err != nil || len(f.Tags) != 1 {
		t.Fatalf("old = %+v, %v", f, err)
	}
	if err := c.DeleteTagRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationExpiryRenew(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	soon, err := c.Upload(ctx, "soon.txt", bytes.NewReader([]byte("soon")), 4, &fileapi.UploadOptions{ExpiresIn: 5 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := c.Upload(ctx, "keep.txt", bytes.NewReader([]byte("keep")), 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := c.List(ctx, &fileapi.ListQuery{ExpiringDays: 7}); err != nil || len(page.List) != 1 || page.List[0].ID != soon.ID {
		t.Fatalf("expiring = %+v, %v", page, err)
	}
	if n, err := c.BatchRenew(ctx, []int64{soon.ID, keep.ID}, 30*24*time.Hour); err != nil || n != 2 {
		t.Fatalf("renew = %d, %v", n, err)
	}
	if page, err := c.List(ctx, &fileapi.ListQuery{ExpiringDays: 7}); err != nil || len(page.List) != 0 {
		t.Fatalf("after renew = %+v, %v", page, err)
	}
	if n, err := c.BatchSetExpiration(ctx, []int64{keep.ID}, time.Time{}); err != nil || n != 1 {
		t.Fatalf("clear = %d, %v", n, err)
	}
	if f, err := c.Get(ctx, keep.ID); err != nil || f.ExpiresAt != nil {
		t.Fatalf("keep = %+v, %v", f, err)
	}
}

// patternPNG 左亮右暗带深色方块的测试图；mirror 时左右镜像
func patternPNG(t *testing.T, w, h int, mirror bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			xx := x
			if mirror {
				xx = w - 1 - x
			}
			v := uint8(255 - xx*200/w)
			if xx > w/3 && xx < w/2 && y > h/4 && y < h/2 {
				v = 20
			}
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIntegrationSimilarImages(t *testing.T) {
	c, _, _ := newTestClient(t)
	ctx := context.Background()
	upload := func(name string, data []byte) int64 {
		t.Helper()
		f, err := c.Upload(ctx, name, bytes.NewReader(data), int64(len(data)), nil)
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	a := upload("a.png", patternPNG(t, 600, 400, false))
	b := upload("b.png", patternPNG(t, 300, 200, false))
	upload("c.png", patternPNG(t, 600, 400, true))
	var res *fileapi.SimilarResult
	var err error
	for i := 0; i < 120; i++ { // 服务端后台每 media.interval 计算一次图片哈希
		if g, gerr := c.SimilarImageGroups(ctx, nil); gerr == nil && g.Scanned == 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if res, err = c.SimilarImages(ctx, a, nil); err != nil || !res.Hashed || len(res.List) != 1 || res.List[0].File.ID != b {
		t.Fatalf("similar = %+v, %v", res, err)
	}
	groups, err := c.SimilarImageGroups(ctx, nil)
	if err != nil || groups.Total != 1 || len(groups.Groups[0].Files) != 2 || groups.Groups[0].Files[0].File.ID != b {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	txt := upload("a.txt", []byte("text"))
	if _, err := c.SimilarImages(ctx, txt, nil); !fileapi.IsCode(err, fileapi.CodeBadRequest) {
		t.Fatalf("non-image: %v", err)
	}
}
