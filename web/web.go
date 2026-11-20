// Package web 统一装配 Gin 引擎、全局中间件、安全组件、健康检查、指标与 HTTP Server。
//
// 启动流程：
//
//	web.Configure(...)   // 可选：注入 SPA 静态资源等
//	boot.Startup(ctx)    // 基础设施与 Web 引擎
//	web.API()            // 业务手动注册模块与路由
//	web.Run(ctx)         // 最后手动启动 HTTP 服务
package web

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/database"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/health"
	"github.com/god-jason/goframe/license"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/monitor"
	"github.com/god-jason/goframe/openapi"
	"github.com/god-jason/goframe/security/csrf"
	"github.com/god-jason/goframe/security/ratelimit"
	"github.com/god-jason/goframe/sysinfo"
	"github.com/god-jason/goframe/trace"
	"github.com/god-jason/goframe/websocket"
)

// WebOptions 是 Web 装配选项。必须在 boot.Startup 前通过 Configure 设置。
type WebOptions struct {
	// FileSystem 是 SPA embed 模式使用的静态资源文件系统。
	FileSystem fs.FS
}

// WebOption 是 Web 装配选项函数。
type WebOption func(*WebOptions)

// WithFileSystem 注入 SPA embed 模式的静态资源。
func WithFileSystem(fsys fs.FS) WebOption {
	return func(o *WebOptions) { o.FileSystem = fsys }
}

var (
	webOptions WebOptions

	global = struct {
		engine  *gin.Engine
		router  *api.Router
		server  *Server
		health  *health.Checker
		metrics *monitor.Metrics
		started bool
	}{}
)

// Configure 设置 Web 装配选项，应在 boot.Startup 前调用。
func Configure(opts ...WebOption) {
	for _, opt := range opts {
		if opt != nil {
			opt(&webOptions)
		}
	}
}

func init() {
	_ = boot.Register("web", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "trace", "license", "database", "cache", "ratelimit", "csrf", "websocket"},
	})
}

// Engine 返回全局 Gin 引擎。
func Engine() *gin.Engine {
	return global.engine
}

// Router 返回路由封装。
func Router() *api.Router {
	return global.router
}

// API 返回 /api/{version} 根分组，业务模块在此注册路由。
func API() *gin.RouterGroup {
	if r := Router(); r != nil {
		return r.API()
	}
	return nil
}

// Health 返回健康检查器。
func Health() *health.Checker {
	return global.health
}

// Metrics 返回 Prometheus 指标。
func Metrics() *monitor.Metrics {
	return global.metrics
}

// Addr 返回实际监听地址。
func Addr() string {
	srv := global.server
	if srv != nil {
		return srv.Addr()
	}
	return ""
}

// Startup 创建 Gin 引擎、中间件、健康检查与 HTTP Server（不监听端口）。
func Startup(ctx context.Context) error {
	_ = ctx
	cfg := GetConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	tlsConfig := CurrentTLSConfig()
	if err := tlsConfig.Validate(); err != nil {
		return err
	}

	gin.SetMode(cfg.Mode)
	engine := gin.New()
	if len(cfg.TrustedProxies) > 0 {
		if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			return fmt.Errorf("server: set trusted proxies: %w", err)
		}
	} else {
		_ = engine.SetTrustedProxies(nil)
	}

	setupMiddleware(engine, *cfg)
	apiRouter := api.New(engine, api.Options{
		APIPrefix:  cfg.APIPrefix,
		APIVersion: cfg.APIVersion,
	})

	checker := health.New(config.App().Version)
	checker.SetLogger(logger.Get())
	if db := database.Get(); db != nil {
		_ = checker.Add(health.DatabaseCheck("database", db))
	}
	if c := cache.Get(); c != nil {
		if pinger, ok := c.(cache.Pinger); ok {
			_ = checker.Add(health.CacheCheck("cache", pinger))
		}
	}
	checker.Register()

	metrics := monitor.NewMetrics()
	api.RegisterRoot(http.MethodGet, "/metrics", metrics.Handler())

	if pprofConfig := PProf(); pprofConfig.Enabled {
		pprof.Register(engine, pprofConfig.Path)
	}

	openapi.Register()
	sysinfo.Register()
	license.Register()

	if hub := websocket.Get(); hub != nil {
		api.RegisterRoot(http.MethodGet, api.NormalizePath(websocket.GetConfig().Path), hub.Handler())
	}

	if err := api.MountRoot(engine); err != nil {
		return fmt.Errorf("server: mount framework routes: %w", err)
	}

	if err := setupNoRoute(engine, *cfg); err != nil {
		return fmt.Errorf("server: setup fallback routes: %w", err)
	}

	httpServer, err := New(Options{
		Config:  *cfg,
		TLS:     *tlsConfig,
		Handler: engine,
		Logger:  logger.Get(),
	})
	if err != nil {
		return fmt.Errorf("server: create http server: %w", err)
	}

	global.engine = engine
	global.router = apiRouter
	global.health = checker
	global.metrics = metrics
	global.server = httpServer
	return nil
}

// Shutdown 停止 HTTP Server。
func Shutdown(ctx context.Context) error {
	checker := global.health
	srv := global.server
	global.started = false

	if checker != nil {
		checker.SetReady(false)
	}
	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// Start 开始监听端口，并标记服务就绪。
func Start() error {
	srv := global.server
	checker := global.health
	if srv == nil {
		return fmt.Errorf("server: http server is not initialized")
	}

	if err := srv.Start(); err != nil {
		return err
	}
	if checker != nil {
		checker.SetReady(true)
	}
	global.started = true
	return nil
}

// Run 启动 HTTP 服务并阻塞，直到 ctx 取消或服务异常退出。
// 业务路由必须在调用 Run 之前注册到 API()。
func Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := Start(); err != nil {
		return err
	}

	srv := global.server

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Wait() }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		return err
	}
}

func setupMiddleware(engine *gin.Engine, cfg Config) {
	handlers := make([]gin.HandlerFunc, 0, 8)
	handlers = append(handlers,
		gin.Recovery(),
		trace.Middleware(),
		csrf.Middleware(csrf.Get()),
	)
	if limiter := ratelimit.Get(); limiter != nil {
		handlers = append(handlers, ratelimit.Middleware(limiter, nil, ratelimit.GetConfig().FailOpen))
	}
	handlers = append(handlers,
		methodFilter(cfg.Request.AllowedMethods),
		requestLimits(cfg.Request),
	)
	engine.Use(handlers...)
}

func setupNoRoute(engine *gin.Engine, cfg Config) error {
	spaCfg := api.SPA()
	if !spaCfg.Enabled {
		engine.NoRoute(func(c *gin.Context) {
			api.Fail(c, apperrors.NotFound("route not found"))
		})
		return nil
	}

	var fsys fs.FS
	switch strings.ToLower(spaCfg.Mode) {
	case "directory":
		fsys = os.DirFS(spaCfg.Path)
	case "embed":
		if webOptions.FileSystem == nil {
			return fmt.Errorf("spa.mode embed requires web.WithFileSystem")
		}
		fsys = webOptions.FileSystem
	default:
		return fmt.Errorf("unsupported spa mode %q", spaCfg.Mode)
	}

	handler, err := api.NewSPAHandler(fsys, apiBasePath(cfg), api.SPAOptions{Index: spaCfg.Index})
	if err != nil {
		return err
	}
	engine.NoRoute(handler)
	return nil
}

func apiBasePath(cfg Config) string {
	prefix := strings.TrimRight(cfg.APIPrefix, "/")
	version := strings.Trim(cfg.APIVersion, "/")
	if version == "" {
		return prefix
	}
	return prefix + "/" + version
}
