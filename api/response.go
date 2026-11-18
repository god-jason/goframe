package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
)

const (
	// RequestIDKey 是 RequestID 在 Gin Context 中的键。
	RequestIDKey = "request_id"
	// HeaderRequestID 是请求 ID 的标准请求头。
	HeaderRequestID = "X-Request-ID"
)

// Response 是统一响应结构。业务成功时 Code 为 0。
type Response[T any] struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      T      `json:"data"`
	RequestID string `json:"request_id"`
}

// PageData 是统一分页结构。
type PageData[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// RequestID 返回当前请求的 RequestID。
func RequestID(c *gin.Context) string {
	if id := c.GetString(RequestIDKey); id != "" {
		return id
	}
	return c.GetHeader(HeaderRequestID)
}

// Success 写入 200 成功响应。
func Success(c *gin.Context, data any) {
	write(c, http.StatusOK, Response[any]{Code: apperrors.CodeOK, Message: "ok", Data: data, RequestID: RequestID(c)})
}

// Created 写入 201 创建成功响应。
func Created(c *gin.Context, data any) {
	write(c, http.StatusCreated, Response[any]{Code: apperrors.CodeOK, Message: "created", Data: data, RequestID: RequestID(c)})
}

// Accepted 写入 202 异步任务已接收响应。
func Accepted(c *gin.Context, data any) {
	write(c, http.StatusAccepted, Response[any]{Code: apperrors.CodeOK, Message: "accepted", Data: data, RequestID: RequestID(c)})
}

// NoContent 写入 204 无响应体。
func NoContent(c *gin.Context) {
	c.AbortWithStatus(http.StatusNoContent)
}

// Fail 将错误转换为统一错误响应。未知错误不会向客户端暴露内部细节。
func Fail(c *gin.Context, err error) {
	appErr := apperrors.From(err)
	if appErr == nil {
		appErr = apperrors.Internal("")
	}
	status := appErr.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	write(c, status, Response[any]{
		Code:      appErr.Code,
		Message:   appErr.Message,
		RequestID: RequestID(c),
	})
	c.Abort()
}

// FailWith 使用指定 HTTP 状态码写入错误响应。
func FailWith(c *gin.Context, status int, code int, message string) {
	write(c, status, Response[any]{Code: code, Message: message, RequestID: RequestID(c)})
	c.Abort()
}

// NewPage 构造分页响应。
func NewPage[T any](list []T, total int64, page, pageSize int) PageData[T] {
	if list == nil {
		list = make([]T, 0)
	}
	return PageData[T]{List: list, Total: total, Page: page, PageSize: pageSize}
}
