package license

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

// Register 把许可状态查询接口登记为框架根路径接口，由 web 启动时统一挂载。
// 未启用或路径为空时不做任何登记。
func Register() {
	cfg := GetConfig()
	if !cfg.Enabled || strings.TrimSpace(cfg.StatusPath) == "" {
		return
	}
	api.RegisterRoot(http.MethodGet, resolvePath(cfg.StatusPath), statusHandler())
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

// resolvePath 规范化路径，空值或根路径回退到默认状态路径。
func resolvePath(path string) string {
	resolved := api.NormalizePath(path)
	if resolved == "/" {
		resolved = api.NormalizePath(DefaultConfig().StatusPath)
	}
	return resolved
}
