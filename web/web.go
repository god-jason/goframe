// Package web 统一装配 Gin 引擎、全局中间件、安全组件、健康检查与 HTTP Server。
//
// 启动流程：
//
//	web.Configure(...)      // 可选：注入 SPA 静态资源等
//	boot.Startup(ctx)       // 基础设施与 web 引擎
//	web.API()               // 业务手动注册模块与路由
//	web.Run(ctx)            // 最后手动启动 HTTP 服务
package web

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/database"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/health"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/middleware"
	"github.com/god-jason/goframe/monitor"
	"github.com/god-jason/goframe/response"
	"github.com/god-jason/goframe/router"
	"github.com/god-jason/goframe/security/csrf"
	"github.com/god-jason/goframe/security/ratelimit"
	"github.com/god-jason/goframe/server"
	"github.com/god-jason/goframe/websocket"
)

// Options 是 web 装配选项。必须在 boot.Startup 前通过 Configure 设置。
type Options struct {
	// FileSystem 是 SPA embed 模式使用的静态资源文件系统。
	FileSystem fs.FS
}

// Option 是 web 装配选项函数。
type Option func(*Options)

// WithFileSystem 注入 SPA embed 模式的静态资源。
func WithFileSystem(fsys fs.FS) Option {
	return func(o *Options) { o.FileSystem = fsys }
}

var (
	options Options

	global = struct {
		sync.RWMutex
		engine  *gin.Engine
		router  *router.Router
		server  *server.Server
		health  *health.Checker
		metrics *monitor.Metrics
		started bool
	}{}
)

// Configure 设置 web 装配选项，应在 boot.Startup 前调用。
func Configure(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
}

func init() {
	_ = boot.Register("web", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "database", "cache", "ratelimit", "csrf", "websocket"},
	})
}

// Engine 返回全局 Gin 引擎。
func Engine() *gin.Engine {
	global.RLock()
	defer global.RUnlock()
	return global.engine
}

// Router 返回路由封装。
func Router() *router.Router {
	global.RLock()
	defer global.RUnlock()
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
	global.RLock()
	defer global.RUnlock()
	return global.health
}

// Metrics 返回 Prometheus 指标。
func Metrics() *monitor.Metrics {
	global.RLock()
	defer global.RUnlock()
	return global.metrics
}

// Addr 返回实际监听地址。
func Addr() string {
	global.RLock()
	srv := global.server
	global.RUnlock()
	if srv != nil {
		return srv.Addr()
	}
	return ""
}

// Startup 创建 Gin 引擎、中间件、健康检查与 HTTP Server（不监听端口）。
func Startup(ctx context.Context) error {
	_ = ctx
	serverCfg := server.Current()
	if err := serverCfg.Validate(); err != nil {
		return err
	}
	tlsCfg := server.CurrentTLSConfig()
	if err := tlsCfg.Validate(); err != nil {
		return err
	}

	gin.SetMode(serverCfg.Mode)
	engine := gin.New()
	if len(serverCfg.TrustedProxies) > 0 {
		if err := engine.SetTrustedProxies(serverCfg.TrustedProxies); err != nil {
			return fmt.Errorf("web: set trusted proxies: %w", err)
		}
	} else {
		_ = engine.SetTrustedProxies(nil)
	}

	if err := setupMiddleware(engine); err != nil {
		return err
	}
	api := router.New(engine, router.Options{
		APIPrefix:  serverCfg.APIPrefix,
		APIVersion: serverCfg.APIVersion,
	})
	_ = api

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
	checker.Register(engine)

	metrics := monitor.NewMetrics()
	engine.GET("/metrics", metrics.Handler())

	if hub := websocket.Get(); hub != nil {
		engine.GET(websocket.Current().Path, hub.Handler())
	}

	if err := setupNoRoute(engine, *serverCfg); err != nil {
		return fmt.Errorf("web: setup fallback routes: %w", err)
	}

	httpServer, err := server.New(server.Options{
		Config:  *serverCfg,
		TLS:     *tlsCfg,
		Handler: engine,
		Logger:  logger.Get(),
	})
	if err != nil {
		return fmt.Errorf("web: create http server: %w", err)
	}

	global.Lock()
	global.engine = engine
	global.router = api
	global.health = checker
	global.metrics = metrics
	global.server = httpServer
	global.Unlock()
	return nil
}

// Shutdown 停止 HTTP Server。
func Shutdown(ctx context.Context) error {
	global.Lock()
	checker := global.health
	srv := global.server
	global.started = false
	global.Unlock()

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
	global.RLock()
	srv := global.server
	checker := global.health
	global.RUnlock()
	if srv == nil {
		return fmt.Errorf("web: server is not initialized")
	}

	if err := srv.Start(); err != nil {
		return err
	}
	if checker != nil {
		checker.SetReady(true)
	}
	global.Lock()
	global.started = true
	global.Unlock()
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

	global.RLock()
	srv := global.server
	global.RUnlock()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Wait() }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		return err
	}
}

func setupMiddleware(engine *gin.Engine) error {
	securityCfg := middleware.Current()

	handlers := []gin.HandlerFunc{
		middleware.RequestID(),
		middleware.Recovery(logger.Get()),
	}
	if securityCfg.Enabled {
		if securityCfg.Headers.Enabled {
			handlers = append(handlers, middleware.SecurityHeaders(securityCfg.Headers))
		}
		handlers = append(handlers, middleware.MethodFilter(securityCfg.Request.AllowedMethods))
		corsHandler, err := middleware.CORS(securityCfg.CORS)
		if err != nil {
			return err
		}
		handlers = append(handlers, corsHandler)
		if limiter := ratelimit.Get(); limiter != nil {
			handlers = append(handlers, middleware.RateLimit(limiter, nil, ratelimit.Current().FailOpen))
		}
		handlers = append(handlers, middleware.RequestLimits(securityCfg.Request))
		handlers = append(handlers, middleware.CSRF(csrf.Get()))
	}
	handlers = append(handlers, middleware.AccessLog(logger.Get(), "/health", "/health/live", "/health/ready", "/metrics"))
	engine.Use(handlers...)
	return nil
}

func setupNoRoute(engine *gin.Engine, serverCfg server.Config) error {
	spaCfg := router.SPA()
	if !spaCfg.Enabled {
		engine.NoRoute(func(c *gin.Context) {
			response.Fail(c, apperrors.NotFound("route not found"))
		})
		return nil
	}

	var fsys fs.FS
	switch strings.ToLower(spaCfg.Mode) {
	case "directory":
		fsys = os.DirFS(spaCfg.Path)
	case "embed":
		if options.FileSystem == nil {
			return fmt.Errorf("spa.mode embed requires web.WithFileSystem")
		}
		fsys = options.FileSystem
	default:
		return fmt.Errorf("unsupported spa mode %q", spaCfg.Mode)
	}

	handler, err := router.NewSPAHandler(fsys, apiBasePath(serverCfg), router.SPAOptions{Index: spaCfg.Index})
	if err != nil {
		return err
	}
	engine.NoRoute(handler)
	return nil
}

func apiBasePath(cfg server.Config) string {
	prefix := strings.TrimRight(cfg.APIPrefix, "/")
	version := strings.Trim(cfg.APIVersion, "/")
	if version == "" {
		return prefix
	}
	return prefix + "/" + version
}
