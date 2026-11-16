package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 创建跨域中间件。生产环境禁止任意 Origin；Cookie credentials 模式
// 不允许通配 Origin。CORS 不作为身份认证和资源授权机制。
func CORS(cfg CORSConfig) (gin.HandlerFunc, error) {
	if !cfg.Enabled {
		return func(c *gin.Context) { c.Next() }, nil
	}

	allowAll := false
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.ToLower(strings.TrimSpace(origin))
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[origin] = struct{}{}
	}
	if allowAll && cfg.AllowCredentials {
		return nil, fmt.Errorf("middleware: cors wildcard origin is not allowed with credentials")
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	exposed := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.Seconds()))

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}

		allowedOrigin := ""
		if allowAll {
			allowedOrigin = "*"
		} else if _, ok := allowed[strings.ToLower(origin)]; ok {
			allowedOrigin = origin
		}
		if allowedOrigin == "" {
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", allowedOrigin)
		c.Header("Vary", "Origin")
		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposed != "" {
			c.Header("Access-Control-Expose-Headers", exposed)
		}

		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", methods)
			if headers != "" {
				c.Header("Access-Control-Allow-Headers", headers)
			} else if requested := c.GetHeader("Access-Control-Request-Headers"); requested != "" {
				c.Header("Access-Control-Allow-Headers", requested)
			}
			if cfg.MaxAge > 0 {
				c.Header("Access-Control-Max-Age", maxAge)
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}, nil
}
