package openapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	swaggoFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"golang.org/x/net/webdav"
)

// Register 按当前配置注册 Swagger UI。
// 未启用时不做任何注册；启用时挂载 UI 资源，并把 Path 重定向到 index.html。
// engine 为空时直接返回，便于业务按需调用。
func Register(engine *gin.Engine) {
	register(engine, *GetConfig())
}

// register 使用显式配置注册，便于测试与自定义装配。
func register(engine *gin.Engine, cfg Config) {
	if engine == nil || !cfg.Enabled {
		return
	}

	path := normalizePath(cfg.Path)
	index := path + "/index.html"
	engine.GET(path, func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, index)
	})
	engine.GET(path+"/*any", ginSwagger.WrapHandler(Handler()))
}

// Handler 返回 Swagger UI 静态资源处理器，业务可自行用 ginSwagger 包装后挂载。
func Handler() *webdav.Handler {
	return swaggoFiles.Handler
}

// normalizePath 补全前导斜杠并去掉尾部斜杠，空值回退到默认路径。
func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfig().Path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = DefaultConfig().Path
	}
	return path
}
