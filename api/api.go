// Package api 提供业务接口的统一注册与集中挂载，并包含统一响应、
// 分页、路由分组与 SPA 托管、认证/租户/权限守卫等 HTTP 接口基础设施。
//
// 业务模块在初始化时通过 Register 系列函数把接口登记到全局注册表，
// 应用入口在 boot.Startup 之后手动调用 Mount，把全部接口挂载到 web 的 API 分组：
//
//	boot.Startup(ctx)
//	demo.New().Init()          // 业务模块内部调用 api.Register*
//	api.Mount(web.API())       // 统一挂载
//	web.Run(ctx)
//
// 与按注册顺序隐式决定鉴权级别不同，每个接口显式携带 Access 级别，
// Mount 时按级别自动加上守卫中间件，注册顺序不影响结果。
package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/permission"
)

// Access 是接口的访问级别。
type Access int

const (
	// Public 无需鉴权即可访问。
	Public Access = iota
	// Login 要求已登录身份。
	Login
	// Admin 要求已登录且通过管理员权限检查。
	Admin
)

// API 是一条已登记的接口。
type API struct {
	Access   Access
	Method   string
	Path     string
	Handlers []gin.HandlerFunc
}

var entries []*API

// Register 登记一个需要登录的接口。
func Register(method, path string, handlers ...gin.HandlerFunc) {
	RegisterAs(Login, method, path, handlers...)
}

// RegisterPublic 登记一个无需鉴权的公开接口。
func RegisterPublic(method, path string, handlers ...gin.HandlerFunc) {
	RegisterAs(Public, method, path, handlers...)
}

// RegisterAdmin 登记一个管理员接口。
func RegisterAdmin(method, path string, handlers ...gin.HandlerFunc) {
	RegisterAs(Admin, method, path, handlers...)
}

// RegisterAs 按指定访问级别登记接口。可在模块 init 或 Init 中调用。
// 同方法同路径重复登记时覆盖先前条目，便于进程内重启与测试。
func RegisterAs(access Access, method, path string, handlers ...gin.HandlerFunc) {
	entry := &API{
		Access:   access,
		Method:   strings.ToUpper(strings.TrimSpace(method)),
		Path:     path,
		Handlers: handlers,
	}
	for i, existing := range entries {
		if existing.Method == entry.Method && existing.Path == entry.Path {
			entries[i] = entry
			return
		}
	}
	entries = append(entries, entry)
}

// Entries 返回已登记接口的快照，供文档生成与自检使用。
func Entries() []*API {
	snapshot := make([]*API, len(entries))
	copy(snapshot, entries)
	return snapshot
}

// Reset 清空注册表，仅供测试使用。
func Reset() { entries = nil }

type mountOptions struct {
	guards map[Access][]gin.HandlerFunc
}

// Option 定制 Mount 使用的守卫中间件。
type Option func(*mountOptions)

// WithGuard 替换指定访问级别的守卫中间件，传入空列表表示不加任何守卫。
func WithGuard(access Access, handlers ...gin.HandlerFunc) Option {
	return func(o *mountOptions) {
		o.guards[access] = handlers
	}
}

// Mount 把全部已登记接口挂载到 group。
//
// 默认守卫：Login 级别要求完成认证（Authenticate 解析令牌 + RequireAuth 强制登录），
// Admin 级别在登录之上再执行 permission 授权检查。
// 必须在 boot.Startup 之后调用，以便 auth.Get() 与 permission.Get() 已就绪。
// 方法或路径非法时返回错误，且不会挂载任何接口。
func Mount(group gin.IRoutes, opts ...Option) error {
	options := &mountOptions{guards: defaultGuards()}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	for _, entry := range entries {
		if !validMethod(entry.Method) {
			return fmt.Errorf("api: invalid method %q for %s", entry.Method, entry.Path)
		}
		if !strings.HasPrefix(entry.Path, "/") {
			return fmt.Errorf("api: path %q must start with /", entry.Path)
		}
	}

	for _, entry := range entries {
		handlers := make([]gin.HandlerFunc, 0, len(options.guards[entry.Access])+len(entry.Handlers))
		handlers = append(handlers, options.guards[entry.Access]...)
		handlers = append(handlers, entry.Handlers...)
		group.Handle(entry.Method, entry.Path, handlers...)
	}
	return nil
}

// defaultGuards 构造默认守卫。认证器与授权器在 Mount 时才读取，
// 因此业务模块的注入与 boot.Startup 的结果都会生效。
func defaultGuards() map[Access][]gin.HandlerFunc {
	login := []gin.HandlerFunc{
		Authenticate(auth.Get()),
		RequireAuth(),
	}
	return map[Access][]gin.HandlerFunc{
		Public: nil,
		Login:  login,
		Admin:  append(append([]gin.HandlerFunc{}, login...), RequirePermission(permission.Get(), "admin", nil)),
	}
}

func validMethod(method string) bool {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return true
	default:
		return false
	}
}
