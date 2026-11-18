// Package middleware 提供框架通用 HTTP 中间件。
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/id"
)

// RequestID 为每个请求生成或校验 X-Request-ID。
// 客户端提供的 Request ID 必须校验格式和长度，不作为身份凭证。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader(api.HeaderRequestID))
		if !validRequestID(requestID) {
			requestID = id.New()
		}
		c.Set(api.RequestIDKey, requestID)
		c.Header(api.HeaderRequestID, requestID)
		c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '-', ch == '_', ch == '.':
		default:
			return false
		}
	}
	return true
}
