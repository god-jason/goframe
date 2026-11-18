package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/trace"
)

// AccessLog 在请求完成后记录访问日志（状态码、耗时、请求 ID）。
func AccessLog(log logger.Logger, skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, path := range skipPaths {
		skip[path] = struct{}{}
	}

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		path := c.Request.URL.Path
		if _, ok := skip[path]; ok {
			return
		}
		if _, ok := skip[route]; ok {
			return
		}

		fields := []logger.Field{
			logger.String("request_id", api.RequestID(c)),
			logger.String("method", c.Request.Method),
			logger.String("path", path),
			logger.String("route", route),
			logger.Int("status", c.Writer.Status()),
			logger.Int("size", c.Writer.Size()),
			logger.Duration("latency", time.Since(start)),
			logger.String("client_ip", c.ClientIP()),
		}
		if identity, ok := auth.FromGin(c); ok {
			fields = append(fields,
				logger.String("user_id", identity.UserID),
				logger.String("tenant_id", identity.TenantID),
			)
		}
		if traceID := trace.TraceID(c.Request.Context()); traceID != "" {
			fields = append(fields, logger.String("trace_id", traceID))
		}
		log.Info("http request", fields...)
	}
}
