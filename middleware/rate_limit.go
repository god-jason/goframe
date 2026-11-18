package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/security/ratelimit"
)

// RateLimit 创建限流中间件。keyFunc 为空时按客户端 IP 限流。
// failOpen 为 true 时，限流组件异常将放行普通请求；高风险接口应失败关闭。
func RateLimit(limiter ratelimit.Limiter, keyFunc func(*gin.Context) string, failOpen bool) gin.HandlerFunc {
	if limiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	if keyFunc == nil {
		keyFunc = ClientIPKey
	}

	return func(c *gin.Context) {
		allowed, err := limiter.Allow(c.Request.Context(), keyFunc(c))
		if err != nil {
			if failOpen {
				c.Next()
				return
			}
			api.Fail(c, apperrors.Unavailable("rate limiter unavailable"))
			return
		}
		if !allowed {
			c.Header("Retry-After", "1")
			api.Fail(c, apperrors.TooManyRequests("too many requests"))
			return
		}
		c.Next()
	}
}

// ClientIPKey 按客户端 IP 限流。
func ClientIPKey(c *gin.Context) string { return "ip:" + c.ClientIP() }

// UserKey 按认证用户限流，未认证时回退到 IP。
func UserKey(c *gin.Context) string {
	if identity, ok := auth.FromGin(c); ok {
		return "user:" + identity.UserID
	}
	return ClientIPKey(c)
}

// TenantKey 按租户限流，未认证时回退到 IP。
func TenantKey(c *gin.Context) string {
	if identity, ok := auth.FromGin(c); ok && identity.TenantID != "" {
		return "tenant:" + identity.TenantID
	}
	return ClientIPKey(c)
}
