package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/response"
)

// SecurityHeaders 设置安全响应头。
func SecurityHeaders(cfg HeadersConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if cfg.HSTS {
			c.Header("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains", int(cfg.HSTSMaxAge.Seconds())))
		}
		if cfg.CSP != "" {
			header := "Content-Security-Policy"
			if cfg.CSPReportOnly {
				header = "Content-Security-Policy-Report-Only"
			}
			c.Header(header, cfg.CSP)
		}
		c.Next()
	}
}

// MethodFilter 拒绝 TRACE、CONNECT 以及未允许的 HTTP 方法。
func MethodFilter(allowed []string) gin.HandlerFunc {
	if len(allowed) == 0 {
		allowed = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions}
	}
	set := make(map[string]struct{}, len(allowed))
	for _, method := range allowed {
		set[strings.ToUpper(strings.TrimSpace(method))] = struct{}{}
	}
	allowHeader := strings.Join(allowed, ", ")

	return func(c *gin.Context) {
		method := c.Request.Method
		if method == http.MethodTrace || method == http.MethodConnect {
			c.Header("Allow", allowHeader)
			response.Fail(c, apperrors.New(apperrors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed"))
			return
		}
		if _, ok := set[method]; !ok {
			c.Header("Allow", allowHeader)
			response.Fail(c, apperrors.New(apperrors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed"))
			return
		}
		c.Next()
	}
}

// RequestLimits 限制 Query 长度、请求体大小并校验 JSON Content-Type。
func RequestLimits(cfg RequestConfig) gin.HandlerFunc {
	maxBody := cfg.MaxBodySize.Bytes()
	return func(c *gin.Context) {
		if cfg.MaxQueryLength > 0 && len(c.Request.URL.RawQuery) > cfg.MaxQueryLength {
			response.Fail(c, apperrors.BadRequest("query string too long"))
			return
		}

		if maxBody > 0 {
			if c.Request.ContentLength > maxBody {
				response.Fail(c, apperrors.PayloadTooLarge())
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
		}

		if cfg.RequireJSONBody {
			switch c.Request.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				hasBody := c.Request.ContentLength > 0 || len(c.Request.TransferEncoding) > 0
				if hasBody {
					contentType := c.ContentType()
					if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
						response.Fail(c, apperrors.New(apperrors.CodeBadRequest, http.StatusUnsupportedMediaType, "content-type must be application/json"))
						return
					}
				}
			}
		}
		c.Next()
	}
}
