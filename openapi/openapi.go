package openapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	swaggoFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"golang.org/x/net/webdav"

	"github.com/god-jason/goframe/api"
)

// Register 按当前配置把 Swagger UI 登记为框架根路径接口，由 web 启动时统一挂载。
// 未启用时不做任何登记；启用时登记 UI 资源，并把 Path 重定向到 index.html。
func Register() {
	register(*GetConfig())
}

// register 使用显式配置登记，便于测试与自定义装配。
func register(cfg Config) {
	if !cfg.Enabled {
		return
	}

	path := resolvePath(cfg.Path)
	index := path + "/index.html"
	api.RegisterRoot(http.MethodGet, path, func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, index)
	})
	api.RegisterRoot(http.MethodGet, path+"/*any", ginSwagger.WrapHandler(Handler()))
}

// Handler 返回 Swagger UI 静态资源处理器，业务可自行用 ginSwagger 包装后挂载。
func Handler() *webdav.Handler {
	return swaggoFiles.Handler
}

// resolvePath 规范化路径，空值或根路径回退到默认路径。
func resolvePath(path string) string {
	resolved := api.NormalizePath(path)
	if resolved == "/" {
		resolved = api.NormalizePath(DefaultConfig().Path)
	}
	return resolved
}
