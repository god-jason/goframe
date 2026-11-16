package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/response"
	"github.com/god-jason/goframe/security/csrf"
)

// CSRF 为 Cookie 认证的非安全方法提供 CSRF 防护。
// 纯 Bearer Token 接口按认证模式跳过 CSRF 校验；未签发 CSRF Cookie 的请求
// 不携带 Cookie 身份，也不存在被其他站点利用的会话，因此直接放行。
func CSRF(protector *csrf.Protector) gin.HandlerFunc {
	if protector == nil {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if c.GetHeader("Authorization") != "" {
			c.Next()
			return
		}
		if _, err := c.Cookie(protector.CookieName()); err != nil {
			c.Next()
			return
		}
		if err := protector.Validate(c); err != nil {
			response.Fail(c, err)
			return
		}
		c.Next()
	}
}
