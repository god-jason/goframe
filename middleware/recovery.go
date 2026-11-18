package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
)

// Recovery 捕获 panic，记录堆栈并向客户端返回安全的 500 响应。
func Recovery(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// 保留 net/http 对 ErrAbortHandler 的约定。
			if err, ok := recovered.(error); ok && err == http.ErrAbortHandler {
				panic(recovered)
			}

			log.Error("panic recovered",
				logger.Any("panic", recovered),
				logger.String("request_id", api.RequestID(c)),
				logger.String("method", c.Request.Method),
				logger.String("path", c.Request.URL.Path),
				logger.String("stack", string(debug.Stack())),
			)

			if c.Writer.Written() {
				c.Abort()
				return
			}
			api.Fail(c, apperrors.Internal(""))
		}()
		c.Next()
	}
}
