// Package app 负责应用生命周期、依赖装配与模块管理。
package app

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/database"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/event"
	"github.com/god-jason/goframe/health"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/middleware"
	"github.com/god-jason/goframe/monitor"
	"github.com/god-jason/goframe/permission"
	"github.com/god-jason/goframe/response"
	"github.com/god-jason/goframe/router"
	"github.com/god-jason/goframe/scheduler"
	"github.com/god-jason/goframe/security/audit"
	"github.com/god-jason/goframe/security/csrf"
	"github.com/god-jason/goframe/security/ratelimit"
	"github.com/god-jason/goframe/server"
	"github.com/god-jason/goframe/storage"
)

type appState int

const (
	stateCreated appState = iota
	stateInitialized
	stateRunning
	stateStopped
)

// App 是整个应用的运行容器，负责装配和管理所有公共服务。
// App 不包含具体业务对象，业务模块通过 Module 注入。
type App struct {
	Config     *config.Config
	Logger     logger.Logger
	Server     *server.Server
	Router     *router.Router
	DB         database.Database
	Cache      cache.Cache
	Storage    storage.Storage
	Auth       auth.Manager
	Permission permission.Authorizer
	Scheduler  scheduler.Scheduler
	Events     event.EventBus
	Auditor    audit.Auditor
	Health     *health.Checker
	Metrics    *monitor.Metrics
	Modules    *ModuleManager

	configFile  string
	fileSystem  fs.FS
	loader      *config.Loader
	limiter     ratelimit.Limiter
	csrf        *csrf.Protector
	watchCancel context.CancelFunc
	state       appState
	mu          sync.Mutex
}

// New 构造应用对象。New 只负责加载配置与构造对象，不监听端口、不启动后台 Goroutine。
func New(opts ...Option) (*App, error) {
	a := &App{Modules: NewModuleManager(), Metrics: monitor.NewMetrics()}
	for _, opt := range opts {
		opt(a)
	}

	if a.Config == nil {
		loaderOpts := make([]config.Option, 0, 1)
		if a.configFile != "" {
			loaderOpts = append(loaderOpts, config.WithFile(a.configFile))
		}
		loader := config.NewLoader(loaderOpts...)
		cfg, err := loader.Load()
		if err != nil {
			return nil, err
		}
		a.Config = cfg
		a.loader = loader
	}
	if err := a.Config.Validate(); err != nil {
		return nil, err
	}

	if a.Logger == nil {
		log, err := logger.New(logger.Options{
			Level:  a.Config.Logger.Level,
			Format: a.Config.Logger.Format,
			Output: a.Config.Logger.Output,
		})
		if err != nil {
			return nil, err
		}
		a.Logger = log
	}
	if a.Health == nil {
		a.Health = health.New(a.Config.App.Version)
	}
	if a.Events == nil {
		a.Events = event.New(a.Logger)
	}
	return a, nil
}

// Register 注册业务模块。必须在 Init 之前调用。
func (a *App) Register(module Module) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stateCreated {
		return fmt.Errorf("app: cannot register module after initialization")
	}
	return a.Modules.Register(module)
}

// Init 初始化基础设施、模块与路由。初始化失败必须终止启动。
func (a *App) Init(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stateCreated {
		return nil
	}
	if err := a.init(ctx); err != nil {
		return err
	}
	a.state = stateInitialized
	return nil
}

func (a *App) init(ctx context.Context) error {
	cfg := a.Config

	if a.DB == nil && cfg.Database.Enabled() {
		db, err := database.Open(cfg.Database, a.Logger)
		if err != nil {
			return err
		}
		a.DB = db
	}
	if a.Cache == nil {
		c, err := cache.New(cfg.Redis)
		if err != nil {
			return err
		}
		a.Cache = c
	}
	if a.Storage == nil {
		s, err := storage.New(cfg.Storage)
		if err != nil {
			return err
		}
		a.Storage = s
	}
	if a.Scheduler == nil {
		s, err := scheduler.New(a.Logger, cfg.Scheduler)
		if err != nil {
			return err
		}
		a.Scheduler = s
	}
	if a.Events == nil {
		a.Events = event.New(a.Logger)
	}
	if a.Auditor == nil {
		if cfg.Security.Audit.Enabled {
			a.Auditor = audit.NewLogAuditor(a.Logger, cfg.Security.Audit.MaxDetailSize.Bytes())
		} else {
			a.Auditor = audit.NopAuditor{}
		}
	}
	if a.Auth == nil && cfg.Security.Auth.Enabled {
		authService, err := auth.NewService(cfg.Security.Auth, auth.NewMemorySessionRepository(), a.Logger)
		if err != nil {
			return err
		}
		a.Auth = authService
	}
	if a.Permission == nil {
		a.Permission = permission.NewRBAC(permission.DefaultPolicy())
	}
	if cfg.Security.RateLimit.Enabled {
		a.limiter = ratelimit.NewKeyedLimiter(cfg.Security.RateLimit.GlobalRate, cfg.Security.RateLimit.GlobalBurst)
	}
	if cfg.Security.CSRF.Enabled {
		protector, err := csrf.New(cfg.Security.CSRF)
		if err != nil {
			return err
		}
		a.csrf = protector
	}

	if a.DB != nil {
		_ = a.Health.Add(health.DatabaseCheck("database", a.DB))
	}
	if pinger, ok := a.Cache.(cache.Pinger); ok {
		_ = a.Health.Add(health.CacheCheck("cache", pinger))
	}

	gin.SetMode(cfg.Server.Mode)
	engine := gin.New()
	if len(cfg.Server.TrustedProxies) > 0 {
		if err := engine.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
			return fmt.Errorf("app: set trusted proxies: %w", err)
		}
	} else {
		_ = engine.SetTrustedProxies(nil)
	}

	if err := a.setupMiddleware(engine); err != nil {
		return err
	}

	// 必须在注册全局中间件之后再创建 API 分组，
	// Gin 分组会快照创建时的中间件链。
	a.Router = router.New(engine, cfg.Server)

	a.Health.Register(engine)
	engine.GET("/metrics", a.Metrics.Handler())

	if err := a.Modules.Init(a); err != nil {
		return err
	}
	if err := a.Modules.RegisterRoutes(a.Router.API()); err != nil {
		return err
	}
	if err := a.setupNoRoute(engine); err != nil {
		return err
	}

	srv, err := server.New(server.Options{
		Config:  cfg.Server,
		TLS:     cfg.TLS,
		Handler: engine,
		Logger:  a.Logger,
	})
	if err != nil {
		return err
	}
	a.Server = srv
	return nil
}

func (a *App) setupMiddleware(engine *gin.Engine) error {
	cfg := a.Config
	handlers := []gin.HandlerFunc{
		middleware.RequestID(),
		middleware.Recovery(a.Logger),
	}
	if cfg.Security.Enabled {
		if cfg.Security.Headers.Enabled {
			handlers = append(handlers, middleware.SecurityHeaders(cfg.Security.Headers))
		}
		handlers = append(handlers, middleware.MethodFilter(cfg.Security.Request.AllowedMethods))
		corsHandler, err := middleware.CORS(cfg.Security.CORS)
		if err != nil {
			return err
		}
		handlers = append(handlers, corsHandler)
		if cfg.Security.RateLimit.Enabled {
			handlers = append(handlers, middleware.RateLimit(a.limiter, func(*gin.Context) string { return "global" }, cfg.Security.RateLimit.FailOpen))
		}
		handlers = append(handlers, middleware.RequestLimits(cfg.Security.Request))
		handlers = append(handlers, middleware.CSRF(a.csrf))
	}
	handlers = append(handlers, middleware.AccessLog(a.Logger, "/health", "/health/live", "/health/ready", "/metrics"))
	if a.Metrics != nil {
		handlers = append(handlers, a.Metrics.Middleware())
	}
	engine.Use(handlers...)
	return nil
}

// setupNoRoute 注册未匹配路由处理器。Gin 的 NoRoute 会自动继承全局中间件。
func (a *App) setupNoRoute(engine *gin.Engine) error {
	cfg := a.Config

	var handler gin.HandlerFunc
	if !cfg.SPA.Enabled {
		handler = func(c *gin.Context) {
			response.Fail(c, apperrors.NotFound("route not found"))
		}
	} else {
		var fsys fs.FS
		switch cfg.SPA.Mode {
		case "directory":
			fsys = os.DirFS(cfg.SPA.Path)
		case "embed":
			if a.fileSystem == nil {
				return fmt.Errorf("app: spa.mode embed requires app.WithFileSystem")
			}
			fsys = a.fileSystem
		default:
			return fmt.Errorf("app: unsupported spa mode %q", cfg.SPA.Mode)
		}

		spaHandler, err := router.NewSPAHandler(fsys, cfg.APIBasePath(), router.SPAOptions{Index: cfg.SPA.Index})
		if err != nil {
			return fmt.Errorf("app: spa handler: %w", err)
		}
		handler = spaHandler
	}

	engine.NoRoute(handler)
	return nil
}

// Run 初始化并启动应用，阻塞直到收到退出信号或服务异常退出。
func (a *App) Run(ctx context.Context) error {
	if err := a.Init(ctx); err != nil {
		return err
	}

	a.mu.Lock()
	switch a.state {
	case stateRunning:
		a.mu.Unlock()
		return fmt.Errorf("app: already running")
	case stateStopped:
		a.mu.Unlock()
		return fmt.Errorf("app: has been stopped")
	}
	a.state = stateRunning
	a.mu.Unlock()

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if a.loader != nil {
		watchCtx, cancel := context.WithCancel(runCtx)
		a.mu.Lock()
		a.watchCancel = cancel
		a.mu.Unlock()
		go func() {
			if err := a.loader.Watch(watchCtx, a.onConfigChange); err != nil {
				a.Logger.Warn("config watch disabled", logger.Error(err))
			}
		}()
	}

	if a.Config.Scheduler.Enabled {
		if err := a.Scheduler.Start(runCtx); err != nil {
			_ = a.shutdown()
			return err
		}
	}
	if err := a.Modules.Start(runCtx); err != nil {
		_ = a.shutdown()
		return err
	}
	if err := a.Server.Start(); err != nil {
		_ = a.shutdown()
		return err
	}

	a.Health.SetReady(true)
	a.Logger.Info("application started",
		logger.String("name", a.Config.App.Name),
		logger.String("version", a.Config.App.Version),
		logger.String("environment", a.Config.App.Environment),
		logger.String("addr", a.Server.Addr()),
	)

	serverErr := make(chan error, 1)
	go func() { serverErr <- a.Server.Wait() }()

	var runErr error
	select {
	case <-runCtx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			runErr = fmt.Errorf("app: http server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.Server.ShutdownTimeout)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// Shutdown 优雅关闭应用：HTTP 服务 -> 模块 -> 定时任务 -> 缓存 -> 数据库 -> 日志。
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.state == stateStopped || a.state == stateCreated {
		a.mu.Unlock()
		return nil
	}
	a.state = stateStopped
	watchCancel := a.watchCancel
	a.mu.Unlock()

	a.Health.SetReady(false)

	var errs []error
	if a.Server != nil {
		if err := a.Server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if a.Modules != nil {
		if err := a.Modules.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if a.Scheduler != nil {
		if err := a.Scheduler.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if watchCancel != nil {
		watchCancel()
	}
	if a.Cache != nil {
		if err := a.Cache.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if a.DB != nil {
		if err := a.DB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if a.Logger != nil {
		if closer, ok := a.Logger.(interface{ Close() error }); ok {
			_ = closer.Close()
		} else {
			_ = a.Logger.Sync()
		}
	}
	return stderrors.Join(errs...)
}

func (a *App) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.Shutdown(ctx)
}

func (a *App) onConfigChange(cfg *config.Config) {
	a.Logger.Info("configuration reloaded")
	if zapLogger, ok := a.Logger.(*logger.ZapLogger); ok {
		if err := zapLogger.SetLevel(cfg.Logger.Level); err != nil {
			a.Logger.Warn("apply log level failed", logger.Error(err))
		}
	}
}

// CSRFProtector 返回 CSRF 保护器，供业务提供 Token 签发接口。
func (a *App) CSRFProtector() *csrf.Protector { return a.csrf }
