package fileapi

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// 服务端单次批量接口的上限，超出时 SDK 自动分批
const (
	maxBatchIDs  = 100
	maxBatchURLs = 200
)

func filePath(id int64, suffix string) string {
	return "/open/v1/files/" + strconv.FormatInt(id, 10) + suffix
}

// Usage 客户端的存储用量与本月流量
func (c *Client) Usage(ctx context.Context) (*Usage, error) {
	var u Usage
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/usage", idempotent: true}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Get 文件详情；文件不存在（或属于其它客户端）时返回 CodeNotFound 错误，可用 IsNotFound 判断
func (c *Client) Get(ctx context.Context, id int64) (*File, error) {
	var f File
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, ""), idempotent: true}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// List 查询一页文件
func (c *Client) List(ctx context.Context, q *ListQuery) (*FilePage, error) {
	return c.list(ctx, "/open/v1/files", q)
}

// ListTrash 查询一页回收站中的文件（参数同 List），文件带 TrashedAt 与 PurgeAt
func (c *Client) ListTrash(ctx context.Context, q *ListQuery) (*FilePage, error) {
	return c.list(ctx, "/open/v1/trash", q)
}

func (c *Client) list(ctx context.Context, path string, q *ListQuery) (*FilePage, error) {
	v, err := q.values()
	if err != nil {
		return nil, err
	}
	var page FilePage
	if err := c.call(ctx, &request{method: http.MethodGet, path: path, query: v, idempotent: true}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// values 列表查询参数（文件列表、回收站与清单导出共用）
func (q *ListQuery) values() (url.Values, error) {
	if q == nil {
		q = &ListQuery{}
	}
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("category", q.Category)
	set("original_filename", q.OriginalFilename)
	set("object_type", q.ObjectType)
	set("object_id", q.ObjectID)
	set("folder", q.Folder)
	if q.Recursive {
		v.Set("recursive", "true")
	}
	if q.Locked != nil {
		v.Set("locked", strconv.FormatBool(*q.Locked))
	}
	for _, id := range q.TagIDs {
		v.Add("tag_id", strconv.FormatInt(id, 10))
	}
	if q.TagAny {
		v.Set("tag_mode", "any")
	}
	if q.ExpiringDays > 0 {
		v.Set("expiring_days", strconv.Itoa(q.ExpiringDays))
	}
	if q.RetentionExpiringDays > 0 {
		v.Set("retention_expiring_days", strconv.Itoa(q.RetentionExpiringDays))
	}
	if !q.StartTime.IsZero() {
		v.Set("start_time", q.StartTime.Format(time.RFC3339))
	}
	if !q.EndTime.IsZero() {
		v.Set("end_time", q.EndTime.Format(time.RFC3339))
	}
	set("sort", q.Sort)
	set("order", q.Order)
	if q.PageSize > 0 {
		v.Set("page_size", strconv.Itoa(q.PageSize))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	} else if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.WithTotal {
		v.Set("with_total", "true")
	}
	if len(q.Metadata) > 0 {
		raw, err := json.Marshal(q.Metadata)
		if err != nil {
			return nil, err
		}
		v.Set("metadata", string(raw))
	}
	for _, k := range q.MetadataKeys {
		v.Add("metadata_key", k)
	}
	for k, n := range map[string]int64{
		"min_width": int64(q.MinWidth), "max_width": int64(q.MaxWidth), "min_height": int64(q.MinHeight), "max_height": int64(q.MaxHeight),
		"min_duration_ms": q.MinDurationMS, "max_duration_ms": q.MaxDurationMS,
	} {
		if n > 0 {
			v.Set(k, strconv.FormatInt(n, 10))
		}
	}
	return v, nil
}

// ListAll 按游标遍历全部符合条件的文件（忽略 q.Page 与 q.Cursor 之外的翻页参数），出错时产出一次错误后结束：
//
//	for f, err := range c.ListAll(ctx, &fileapi.ListQuery{Category: fileapi.CategoryImage}) {
//		if err != nil { return err }
//		fmt.Println(f.ID)
//	}
func (c *Client) ListAll(ctx context.Context, q *ListQuery) iter.Seq2[*File, error] {
	return func(yield func(*File, error) bool) {
		var cur ListQuery
		if q != nil {
			cur = *q
		}
		cur.Page, cur.WithTotal = 0, false
		if cur.PageSize == 0 {
			cur.PageSize = 100
		}
		for {
			page, err := c.List(ctx, &cur)
			if err != nil {
				yield(nil, err)
				return
			}
			for i := range page.List {
				if !yield(&page.List[i], nil) {
					return
				}
			}
			if !page.HasMore || page.NextCursor == "" {
				return
			}
			cur.Cursor = page.NextCursor
		}
	}
}

// BatchGet 按 ID 批量查询，不存在的 ID 被忽略；超过 100 个时自动分批
func (c *Client) BatchGet(ctx context.Context, ids []int64) ([]File, error) {
	out := make([]File, 0, len(ids))
	for chunk := range chunks(ids, maxBatchIDs) {
		var list []File
		req := &request{method: http.MethodPost, path: "/open/v1/files/batch-get", json: jsonBody(map[string]any{"ids": chunk}), idempotent: true}
		if err := c.call(ctx, req, &list); err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	return out, nil
}

// BatchDelete 批量删除（规则同 Delete，服务端开启回收站时移入回收站）；超过 100 个时自动分批。
// 单个文件删除失败记录在 Failed 中，不中断其余文件
func (c *Client) BatchDelete(ctx context.Context, ids []int64) (*BatchDeleteResult, error) {
	return c.batchDelete(ctx, ids, false)
}

// BatchDeletePermanently 批量彻底删除（不进回收站，也用于彻底删除回收站中的文件）
func (c *Client) BatchDeletePermanently(ctx context.Context, ids []int64) (*BatchDeleteResult, error) {
	return c.batchDelete(ctx, ids, true)
}

func (c *Client) batchDelete(ctx context.Context, ids []int64, permanent bool) (*BatchDeleteResult, error) {
	out := &BatchDeleteResult{Deleted: []int64{}, Trashed: []int64{}, Failed: map[int64]string{}}
	for chunk := range chunks(ids, maxBatchIDs) {
		var res BatchDeleteResult
		body := map[string]any{"ids": chunk}
		if permanent {
			body["permanent"] = true
		}
		req := &request{method: http.MethodPost, path: "/open/v1/files/batch-delete", json: jsonBody(body)}
		if err := c.call(ctx, req, &res); err != nil {
			return out, err
		}
		out.Deleted = append(out.Deleted, res.Deleted...)
		out.Trashed = append(out.Trashed, res.Trashed...)
		for id, reason := range res.Failed {
			out.Failed[id] = reason
		}
	}
	return out, nil
}

// ResolveNames 按文件地址批量反查原始文件名（地址 → 原始文件名，查不到的地址不在结果中）；超过 200 个时自动分批
func (c *Client) ResolveNames(ctx context.Context, urls []string) (map[string]string, error) {
	out := map[string]string{}
	for chunk := range chunks(urls, maxBatchURLs) {
		var names map[string]string
		req := &request{method: http.MethodPost, path: "/open/v1/files/resolve-names", json: jsonBody(map[string]any{"urls": chunk}), idempotent: true}
		if err := c.call(ctx, req, &names); err != nil {
			return nil, err
		}
		for k, v := range names {
			out[k] = v
		}
	}
	return out, nil
}

// Rename 修改原始文件名（下载时使用的文件名）
func (c *Client) Rename(ctx context.Context, id int64, originalFilename string) (*File, error) {
	var f File
	req := &request{method: http.MethodPatch, path: filePath(id, ""), json: jsonBody(map[string]string{"original_filename": originalFilename}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// UpdateMetadata 修改元数据：与已有元数据合并，set 中的键设置为对应的值，remove 中的键删除，其余键不变（修改后最多 20 项）
func (c *Client) UpdateMetadata(ctx context.Context, id int64, set map[string]string, remove ...string) (*File, error) {
	patch := make(map[string]*string, len(set)+len(remove))
	for k, v := range set {
		patch[k] = &v
	}
	for _, k := range remove {
		patch[k] = nil
	}
	var f File
	req := &request{method: http.MethodPatch, path: filePath(id, ""), json: jsonBody(map[string]any{"metadata": patch}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// SetExpiration 设置过期时间，到期后服务端自动删除；传零值取消过期
func (c *Client) SetExpiration(ctx context.Context, id int64, expiresAt time.Time) (*File, error) {
	value := ""
	if !expiresAt.IsZero() {
		value = expiresAt.Format(time.RFC3339)
	}
	var f File
	req := &request{method: http.MethodPatch, path: filePath(id, ""), json: jsonBody(map[string]string{"expires_at": value}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// BatchRenew 批量续期：把最多 1000 个文件的过期时间设为从现在起 d 之后（向上取整到秒），返回修改的文件数（不存在的文件不计）
func (c *Client) BatchRenew(ctx context.Context, ids []int64, d time.Duration) (int64, error) {
	if d <= 0 {
		return 0, fmt.Errorf("fileapi: renew duration must be positive")
	}
	return c.batchExpiry(ctx, map[string]any{"ids": ids, "expires_in": expiresIn(d)})
}

// BatchSetExpiration 批量把文件的过期时间设为 expiresAt（须晚于当前时间）；零值表示取消过期。返回修改的文件数
func (c *Client) BatchSetExpiration(ctx context.Context, ids []int64, expiresAt time.Time) (int64, error) {
	value := ""
	if !expiresAt.IsZero() {
		value = expiresAt.Format(time.RFC3339)
	}
	return c.batchExpiry(ctx, map[string]any{"ids": ids, "expires_at": value})
}

func (c *Client) batchExpiry(ctx context.Context, body map[string]any) (int64, error) {
	var res struct {
		Updated int64 `json:"updated"`
	}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/batch-expiry", json: jsonBody(body), idempotent: true}, &res); err != nil {
		return 0, err
	}
	return res.Updated, nil
}

// expiresIn 过期时长换算为秒（向上取整），不过期返回 0
func expiresIn(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Second - 1) / time.Second)
}

// Delete 删除文件，访问链接立即失效。服务端开启回收站时文件移入回收站（对象保留到期满，期间可用 Restore 恢复，
// 不占用配额），否则彻底删除（存储对象随后异步删除）
func (c *Client) Delete(ctx context.Context, id int64) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: filePath(id, "")}, nil)
}

// DeletePermanently 彻底删除文件（不进回收站）；也用于彻底删除回收站中的文件
func (c *Client) DeletePermanently(ctx context.Context, id int64) error {
	return c.call(ctx, &request{method: http.MethodDelete, path: filePath(id, ""), query: url.Values{"permanent": {"true"}}}, nil)
}

// Restore 恢复回收站中的文件：恢复后重新计入配额（超出时返回 CodeQuotaExceeded），之前签发且未过期的访问链接重新可用
func (c *Client) Restore(ctx context.Context, id int64) (*File, error) {
	var f File
	if err := c.call(ctx, &request{method: http.MethodPost, path: filePath(id, "/restore")}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// EmptyTrash 清空回收站：文件由服务端在接下来的清理中彻底删除（完成前仍可恢复），返回安排删除的文件数
func (c *Client) EmptyTrash(ctx context.Context) (int64, error) {
	var res struct {
		Scheduled int64 `json:"scheduled"`
	}
	if err := c.call(ctx, &request{method: http.MethodDelete, path: "/open/v1/trash", idempotent: true}, &res); err != nil {
		return 0, err
	}
	return res.Scheduled, nil
}

// AccessURL 签发访问链接，默认统一入口链接（opts 可为 nil）
func (c *Client) AccessURL(ctx context.Context, id int64, opts *URLOptions) (*AccessURL, error) {
	v := url.Values{}
	if opts != nil {
		if opts.Mode != "" {
			v.Set("mode", opts.Mode)
		}
		switch {
		case opts.Permanent:
			v.Set("expires", "0")
		case opts.Expires > 0:
			v.Set("expires", strconv.FormatInt(int64(math.Ceil(opts.Expires.Seconds())), 10))
		}
		if opts.Download {
			v.Set("download", "true")
		}
		if opts.Thumbnail {
			v.Set("thumbnail", "true")
		}
		if opts.Preview {
			v.Set("preview", "pdf")
		}
		if img := opts.Image; img != nil {
			for k, n := range map[string]int{"w": img.Width, "h": img.Height, "quality": img.Quality} {
				if n > 0 {
					v.Set(k, strconv.Itoa(n))
				}
			}
			if img.Fit != "" {
				v.Set("fit", img.Fit)
			}
			if img.Format != "" {
				v.Set("format", img.Format)
			}
			if wm := img.Watermark; wm != nil {
				for k, s := range map[string]string{"wm_text": wm.Text, "wm_pos": wm.Position, "wm_color": wm.Color} {
					if s != "" {
						v.Set(k, s)
					}
				}
				if wm.ImageID > 0 {
					v.Set("wm_image", strconv.FormatInt(wm.ImageID, 10))
				}
				for k, n := range map[string]int{"wm_opacity": wm.Opacity, "wm_size": wm.Size} {
					if n > 0 {
						v.Set(k, strconv.Itoa(n))
					}
				}
			}
		}
	}
	if opts != nil {
		if opts.MaxVisits > 0 {
			v.Set("max_visits", strconv.FormatInt(opts.MaxVisits, 10))
		}
		if opts.Revocable {
			v.Set("revocable", "true")
		}
		if opts.Password != "" {
			v.Set("password", opts.Password)
		}
	}
	var u AccessURL
	if err := c.call(ctx, &request{method: http.MethodGet, path: filePath(id, "/url"), query: v, idempotent: true}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// chunks 按 n 个一组切分，空切片不产出
func chunks[T any](s []T, n int) iter.Seq[[]T] {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			k := min(n, len(s))
			if !yield(s[:k]) {
				return
			}
			s = s[k:]
		}
	}
}
