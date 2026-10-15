package fileapi

import (
	"errors"
	"fmt"
)

// Error FileApi 返回的错误（HTTP 状态码 + 业务码），可用 errors.As 取出
type Error struct {
	// StatusCode HTTP 状态码
	StatusCode int
	// Code 业务码，见服务端 README 的错误码表（如 40401 文件不存在）
	Code      int
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("fileapi: %d %s (http %d, request_id %s)", e.Code, e.Message, e.StatusCode, e.RequestID)
	}
	return fmt.Sprintf("fileapi: %d %s (http %d)", e.Code, e.Message, e.StatusCode)
}

// 常用业务码
const (
	CodeBadRequest       = 40001
	CodeMissingAuth      = 40101
	CodeInvalidClient    = 40102
	CodeTimestampExpired = 40103
	CodeNonceReplay      = 40104
	CodeSignature        = 40105
	// CodeLinkPasswordRequired 访问链接需要提取码
	CodeLinkPasswordRequired = 40109
	CodeIPForbidden          = 40301
	CodeClientDisabled       = 40302
	// CodeLinkPasswordWrong 提取码错误
	CodeLinkPasswordWrong = 40305
	// CodeShareNoDownload 分享只允许在线预览，不能下载
	CodeShareNoDownload = 40306
	// CodeCollectionClosed 文件收集已关闭
	CodeCollectionClosed = 40307
	CodeNotFound         = 40401
	CodeConflict         = 40901
	CodeLinkExpired      = 41001
	// CodeLinkExhausted 限次链接的访问次数已用完
	CodeLinkExhausted = 41002
	// CodeLinkRevoked 访问链接已被吊销
	CodeLinkRevoked         = 41003
	CodeFileTooLarge        = 41301
	CodeQuotaExceeded       = 41302
	CodeFileTypeNotAllowed  = 41501
	CodeRangeNotSatisfiable = 41601
	// CodeFileInfected 文件未通过病毒扫描（消息带病毒名）
	CodeFileInfected = 42201
	// CodeScanPending 文件正在进行病毒扫描（服务端开启 scan.block_pending 时），稍后再访问
	CodeScanPending = 42301
	// CodeFileLocked 文件处于保留锁定期（法律保留或保留期限未到），不能删除、上传新版本、恢复或删除历史版本
	CodeFileLocked      = 42302
	CodeTooManyRequests = 42901
	// CodeTrafficExceeded 超出客户端本月下载流量配额（下月 1 日恢复）
	CodeTrafficExceeded = 42903
	// CodeLinkPasswordLocked 提取码错误次数过多，链接暂时锁定
	CodeLinkPasswordLocked = 42904
	// CodeTranscodeExceeded 超出客户端本月转码分钟配额（下月 1 日恢复）
	CodeTranscodeExceeded  = 42905
	CodeStorage            = 50001
	CodeStorageUnavailable = 50002
	// CodeScanUnavailable 病毒扫描服务暂不可用（服务端开启 scan.fail_closed 时）
	CodeScanUnavailable = 50004
)

// IsCode 错误是否为指定业务码
func IsCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// IsNotFound 文件不存在（含访问其它客户端的文件）
func IsNotFound(err error) bool { return IsCode(err, CodeNotFound) }

// StorageError 直传时存储平台拒绝了上传（预签名过期、跨域或大小不符等）
type StorageError struct {
	StatusCode int
	Body       string
}

func (e *StorageError) Error() string {
	return fmt.Sprintf("fileapi: storage rejected the upload (http %d): %s", e.StatusCode, e.Body)
}
