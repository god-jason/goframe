// Package errors 定义框架统一的错误类型与错误处理规则。
package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
)

// AppError 是框架统一的应用错误。
// Code 为稳定的业务错误码，Status 为对外 HTTP 状态码，Cause 保留底层错误供日志排查。
type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
	Cause   error  `json:"-"`
}

// Error 实现 error 接口，包含底层原因，仅用于日志。
func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap 返回底层错误。
func (e *AppError) Unwrap() error { return e.Cause }

// New 创建应用错误。
func New(code, status int, message string) *AppError {
	return &AppError{Code: code, Status: status, Message: message}
}

// WithCause 附加底层错误。
func (e *AppError) WithCause(err error) *AppError {
	clone := *e
	clone.Cause = err
	return &clone
}

// WithMessage 覆盖对外错误信息。
func (e *AppError) WithMessage(message string) *AppError {
	clone := *e
	clone.Message = message
	return &clone
}

// BadRequest 创建参数错误。
func BadRequest(message string) *AppError {
	return New(CodeBadRequest, http.StatusBadRequest, message)
}

// Validation 创建参数校验错误。
func Validation(message string) *AppError {
	return New(CodeValidationFailed, http.StatusBadRequest, message)
}

// Unauthorized 创建未认证错误。
func Unauthorized(message string) *AppError {
	return New(CodeUnauthorized, http.StatusUnauthorized, message)
}

// TokenExpired 创建 Token 过期错误。
func TokenExpired() *AppError {
	return New(CodeTokenExpired, http.StatusUnauthorized, "token expired")
}

// Forbidden 创建无权限错误。
func Forbidden(message string) *AppError {
	return New(CodeForbidden, http.StatusForbidden, message)
}

// PermissionDenied 创建权限不足错误。
func PermissionDenied() *AppError {
	return New(CodePermissionDenied, http.StatusForbidden, "permission denied")
}

// NotFound 创建资源不存在错误。
func NotFound(message string) *AppError {
	return New(CodeNotFound, http.StatusNotFound, message)
}

// Conflict 创建资源冲突错误。
func Conflict(message string) *AppError {
	return New(CodeConflict, http.StatusConflict, message)
}

// TooManyRequests 创建限流错误。
func TooManyRequests(message string) *AppError {
	return New(CodeTooManyRequests, http.StatusTooManyRequests, message)
}

// PayloadTooLarge 创建请求体过大错误。
func PayloadTooLarge() *AppError {
	return New(CodePayloadTooLarge, http.StatusRequestEntityTooLarge, "request body too large")
}

// Timeout 创建请求超时错误。
func Timeout() *AppError {
	return New(CodeTimeout, http.StatusGatewayTimeout, "request timeout")
}

// Unavailable 创建服务不可用错误。
func Unavailable(message string) *AppError {
	return New(CodeUnavailable, http.StatusServiceUnavailable, message)
}

// Internal 创建内部错误。对外只返回安全信息，不暴露底层细节。
func Internal(message string) *AppError {
	if message == "" {
		message = "internal server error"
	}
	return New(CodeInternal, http.StatusInternalServerError, message)
}

// From 将任意错误转换为 *AppError。
// 未知错误统一转换为内部错误，避免数据库、堆栈等细节泄漏给客户端。
func From(err error) *AppError {
	if err == nil {
		return nil
	}

	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr
	}

	var maxBytesErr *http.MaxBytesError
	if stderrors.As(err, &maxBytesErr) {
		return PayloadTooLarge().WithCause(err)
	}

	switch {
	case stderrors.Is(err, context.DeadlineExceeded):
		return Timeout().WithCause(err)
	case stderrors.Is(err, context.Canceled):
		return New(CodeClientClosed, 499, "client closed request").WithCause(err)
	}

	return Internal("").WithCause(err)
}

// Is 判断错误链中是否包含指定错误码。
func Is(err error, code int) bool {
	appErr := From(err)
	return appErr != nil && appErr.Code == code
}
