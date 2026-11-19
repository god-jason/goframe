package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

// methodFilter 拒绝 TRACE、CONNECT 以及未允许的 HTTP 方法。
func methodFilter(allowed []string) gin.HandlerFunc {
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
			api.Fail(c, apperrors.New(apperrors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed"))
			return
		}
		if _, ok := set[method]; !ok {
			c.Header("Allow", allowHeader)
			api.Fail(c, apperrors.New(apperrors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed"))
			return
		}
		c.Next()
	}
}

// requestLimits 限制 Query 长度、请求体大小并校验 JSON Content-Type。
func requestLimits(cfg RequestConfig) gin.HandlerFunc {
	maxBody := cfg.MaxBodySize.Bytes()
	return func(c *gin.Context) {
		if cfg.MaxQueryLength > 0 && len(c.Request.URL.RawQuery) > cfg.MaxQueryLength {
			api.Fail(c, apperrors.BadRequest("query string too long"))
			return
		}

		if maxBody > 0 {
			if c.Request.ContentLength > maxBody {
				api.Fail(c, apperrors.PayloadTooLarge())
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
						api.Fail(c, apperrors.New(apperrors.CodeBadRequest, http.StatusUnsupportedMediaType, "content-type must be application/json"))
						return
					}
				}
			}
		}
		c.Next()
	}
}
