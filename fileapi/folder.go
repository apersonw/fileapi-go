package fileapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// 文件夹（虚拟目录）：文件的 Folder 字段记录所在目录，目录随文件存在、不单独创建；移动文件与重命名目录只修改目录字段，
// 不改动存储对象（对象 Key 与链接不变）。上传时用 UploadOptions.Folder 等指定目录，用 ListQuery.Folder 列出目录中的文件。

// Folder 子目录：其中（含下级目录）正常文件的数量与总大小
type Folder struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

// FolderList 目录的直接子目录，以及直接位于该目录中的文件数与总大小
type FolderList struct {
	Path    string   `json:"path"`
	Folders []Folder `json:"folders"`
	Files   int64    `json:"files"`
	Bytes   int64    `json:"bytes"`
}

// FolderDeleteResult 删除目录的结果：Remaining 大于 0 时再次调用 DeleteFolder
type FolderDeleteResult struct {
	Deleted   int `json:"deleted"`
	Trashed   int `json:"trashed"`
	Failed    int `json:"failed"`
	Remaining int `json:"remaining"`
}

// ListFolders 列出 parent 目录（空为根目录）的直接子目录
func (c *Client) ListFolders(ctx context.Context, parent string) (*FolderList, error) {
	q := url.Values{}
	if parent != "" {
		q.Set("parent", parent)
	}
	var res FolderList
	if err := c.call(ctx, &request{method: http.MethodGet, path: "/open/v1/folders", query: q, idempotent: true}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// MoveFiles 把文件移到目录 folder（"/" 为根目录），不存在或不属于本客户端的 ID 忽略，返回移动的文件数
func (c *Client) MoveFiles(ctx context.Context, ids []int64, folder string) (int64, error) {
	if len(ids) == 0 {
		return 0, errors.New("fileapi: ids is required")
	}
	var res struct {
		Moved int64 `json:"moved"`
	}
	body := map[string]any{"ids": ids, "folder": folder}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/files/move", json: jsonBody(body), idempotent: true}, &res); err != nil {
		return 0, err
	}
	return res.Moved, nil
}

// MoveFile 把单个文件移到目录 folder
func (c *Client) MoveFile(ctx context.Context, id int64, folder string) (*File, error) {
	var f File
	req := &request{method: http.MethodPatch, path: filePath(id, ""), json: jsonBody(map[string]string{"folder": folder}), idempotent: true}
	if err := c.call(ctx, req, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// RenameFolder 重命名 / 移动目录：其中（含下级目录、回收站中）的文件改到新目录下，返回修改的文件数。
// 不能重命名根目录，不能移到自己的下级目录中，目标已有文件时合并
func (c *Client) RenameFolder(ctx context.Context, from, to string) (int64, error) {
	var res struct {
		Moved int64 `json:"moved"`
	}
	body := map[string]string{"from": from, "to": to}
	if err := c.call(ctx, &request{method: http.MethodPost, path: "/open/v1/folders/rename", json: jsonBody(body)}, &res); err != nil {
		return 0, err
	}
	return res.Moved, nil
}

// DeleteFolder 删除目录中（含下级目录）的文件，规则同 Delete（开启回收站时移入回收站，permanent 时彻底删除）；
// 单次最多删除 1000 个，Remaining 大于 0 时再次调用
func (c *Client) DeleteFolder(ctx context.Context, path string, permanent bool) (*FolderDeleteResult, error) {
	q := url.Values{"path": {path}}
	if permanent {
		q.Set("permanent", strconv.FormatBool(permanent))
	}
	var res FolderDeleteResult
	if err := c.call(ctx, &request{method: http.MethodDelete, path: "/open/v1/folders", query: q}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
