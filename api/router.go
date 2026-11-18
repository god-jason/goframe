package api

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// Router 封装 API 根分组。路由分组只用于组织接口和挂载中间件，
// 不代表最终资源授权。
type Router struct {
	engine *gin.Engine
	cfg    Options
	api    *gin.RouterGroup
}

// New 创建路由封装。
func New(engine *gin.Engine, opts Options) *Router {
	r := &Router{engine: engine, cfg: opts}
	r.api = engine.Group(joinPath(opts.APIPrefix, opts.APIVersion))
	return r
}

// Engine 返回底层 Gin 引擎。
func (r *Router) Engine() *gin.Engine { return r.engine }

// API 返回 /api/{version} 根分组。
func (r *Router) API() *gin.RouterGroup { return r.api }

// Group 在 API 根分组下创建子分组。
func (r *Router) Group(relativePath string, handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.api.Group(joinPath("", relativePath), handlers...)
}

// Public 返回公开接口分组。
func (r *Router) Public(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/public", handlers...)
}

// Auth 返回登录认证分组。
func (r *Router) Auth(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/auth", handlers...)
}

// User 返回用户接口分组。
func (r *Router) User(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/user", handlers...)
}

// Admin 返回管理接口分组。
func (r *Router) Admin(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/admin", handlers...)
}

// Tenant 返回租户接口分组。
func (r *Router) Tenant(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/tenant", handlers...)
}

// System 返回系统接口分组。
func (r *Router) System(handlers ...gin.HandlerFunc) *gin.RouterGroup {
	return r.Group("/system", handlers...)
}

// NoRoute 注册未匹配路由处理器。
func (r *Router) NoRoute(handlers ...gin.HandlerFunc) {
	r.engine.NoRoute(handlers...)
}

// joinPath 拼接路径并清理多余的斜杠。
func joinPath(parts ...string) string {
	var segments []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments = append(segments, strings.Trim(part, "/"))
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/")
}
