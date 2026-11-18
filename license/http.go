package license

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

// Register 注册许可状态查询接口。未启用或路径为空时不做任何注册。
func Register(engine *gin.Engine) {
	cfg := GetConfig()
	if engine == nil || !cfg.Enabled || strings.TrimSpace(cfg.StatusPath) == "" {
		return
	}
	path := normalizePath(cfg.StatusPath)
	engine.GET(path, statusHandler())
}

// RequireValid 要求当前许可有效（含宽限期）。
func RequireValid() gin.HandlerFunc {
	return func(c *gin.Context) {
		manager := Get()
		if manager == nil {
			api.Fail(c, apperrors.Unavailable("license is not loaded"))
			return
		}
		if !manager.Valid() {
			api.Fail(c, apperrors.Forbidden("license is not valid"))
			return
		}
		c.Next()
	}
}

// RequireFeature 要求当前许可包含指定功能。
// 许可系统未启用或功能未授权时返回 403。
func RequireFeature(feature string) gin.HandlerFunc {
	feature = strings.TrimSpace(feature)
	return func(c *gin.Context) {
		manager := Get()
		if manager == nil {
			api.Fail(c, apperrors.Forbidden("license does not include feature: "+feature))
			return
		}
		if !manager.HasFeature(feature) {
			api.Fail(c, apperrors.Forbidden("license does not include feature: "+feature))
			return
		}
		c.Next()
	}
}

// statusHandler 返回许可状态快照。
func statusHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		manager := Get()
		if manager == nil {
			api.Fail(c, apperrors.Unavailable("license is not loaded"))
			return
		}
		api.Success(c, manager.Snapshot())
	}
}

// normalizePath 补全前导斜杠并去掉尾部斜杠。
func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfig().StatusPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = DefaultConfig().StatusPath
	}
	return path
}
