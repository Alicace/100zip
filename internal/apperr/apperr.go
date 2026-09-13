// Package apperr 定义全局错误码与错误类型。
// 错误码必须与 docs/06-接口与数据模型.md 第 5 节保持一致；新增码需同步文档。
package apperr

import "fmt"

// Code 是面向用户与前端契约的错误码。
type Code string

const (
	CodeAuthRequired      Code = "AUTH_REQUIRED"
	CodePathNotAuthorized Code = "PATH_NOT_AUTHORIZED"
	CodeACLDenied         Code = "ACL_DENIED"
	CodePathNotFound      Code = "PATH_NOT_FOUND"
	CodePathIsVolumeTail  Code = "PATH_IS_VOLUME_TAIL"
	CodeDestNotWritable   Code = "DEST_NOT_WRITABLE"
	CodeDestNotEmpty      Code = "DEST_NOT_EMPTY"
	CodeUnsupportedFormat Code = "UNSUPPORTED_FORMAT"
	CodeArchiveCorrupt    Code = "ARCHIVE_CORRUPT"
	CodeMissingVolume     Code = "MISSING_VOLUME"
	CodePasswordRequired  Code = "PASSWORD_REQUIRED"
	CodePasswordWrong     Code = "PASSWORD_WRONG"
	CodeCodepageSuspect   Code = "CODEPAGE_SUSPECT"
	CodeDiskFull          Code = "DISK_FULL"
	CodeExpansionLimit    Code = "EXPANSION_LIMIT"
	CodeJobNotFound       Code = "JOB_NOT_FOUND"
	CodeJobCancelled      Code = "JOB_CANCELLED"
	CodeEngineMissing     Code = "ENGINE_MISSING"
	CodeEngineFailed      Code = "ENGINE_FAILED"
	CodeInternal          Code = "INTERNAL"
)

// Error 是带错误码、用户提示与修复建议的应用错误。
type Error struct {
	Code    Code
	Message string
	Hint    string
	Detail  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New 构造一个应用错误。
func New(code Code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint}
}

// Wrap 在应用错误上附加内部细节；未知错误直接以 err.Error() 作为用户可见信息，
// 避免「内部错误」掩盖真实原因（预览失败等场景）。
func Wrap(err error, detail string) *Error {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*Error); ok {
		cp := *ae
		if detail != "" {
			cp.Detail = detail
		}
		return &cp
	}
	return &Error{Code: CodeInternal, Message: err.Error(), Hint: "请提交诊断信息", Detail: detail + ": " + err.Error()}
}

// 常用错误快捷构造。
func NotAuthorized() *Error {
	return New(CodePathNotAuthorized, "尚未授权访问该位置", "点击「选择目录」完成授权")
}
func NotFound() *Error {
	return New(CodePathNotFound, "找不到该文件或目录", "确认文件是否被移动或删除")
}
func EngineMissing() *Error {
	return New(CodeEngineMissing, "解压引擎不可用", "重装应用或反馈诊断信息")
}
