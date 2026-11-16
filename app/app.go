// Package app 负责应用生命周期、依赖装配与模块管理。
package app

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"runtime/debug"
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
	"github.com/god-jason/goframe/pool"
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
//
// 字段分为三类：
//   - 配置与日志：Config、Logger；
//   - 基础设施：DB、Cache、Storage、Scheduler、Events、Auditor、Pool；
//   - 安全与接入：Auth、Permission、Router、Server、Health、Metrics、Modules。
//
// App 不包含具体业务对象，业务模块通过 Module 注入；所有基础设施均可通过
// Option 替换，便于测试与定制。App 本身并发安全，但 Init/Run/Shutdown
// 应按顺序调用。
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
	Pool       *pool.Pool
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
	a.Health.SetLogger(a.Logger)
	if a.Modules != nil {
		a.Modules.SetLogger(a.Logger)
	}
	if a.Events == nil {
		a.Events = event.New(a.Logger)
	}

	configSource := "in-memory"
	if a.loader != nil && a.loader.Path() != "" {
		configSource = a.loader.Path()
	}
	a.Logger.Info("application configured",
		logger.String("name", a.Config.App.Name),
		logger.String("version", a.Config.App.Version),
		logger.String("environment", a.Config.App.Environment),
		logger.String("config_source", configSource),
	)
	return a, nil
}

// Register 注册业务模块。必须在 Init 之前调用。
func (a *App) Register(module Module) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stateCreated {
		return fmt.Errorf("app: cannot register module after initialization")
	}
	if err := a.Modules.Register(module); err != nil {
		a.Logger.Warn("module registration failed", logger.Error(err))
		return err
	}
	a.Logger.Info("module registered", logger.String("module", module.Name()))
	return nil
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

// init 按固定顺序装配基础设施。顺序很重要：
// 配置 -> 数据库/缓存/存储/调度器/协程池 -> 审计/认证/授权/限流 -> 路由 -> 模块 -> Server。
// 任一阶段失败立即返回，不允许带着不完整依赖进入运行状态。
func (a *App) init(ctx context.Context) error {
	cfg := a.Config
	started := time.Now()
	a.Logger.Info("application initialization started")

	// 1. 数据库：未配置 driver 时不连接，便于纯内存/边缘部署。
	if a.DB == nil && cfg.Database.Enabled() {
		stage := time.Now()
		db, err := database.Open(cfg.Database, a.Logger)
		if err != nil {
			return fmt.Errorf("app: initialize database: %w", err)
		}
		a.DB = db
		a.Logger.Info("database initialized",
			logger.String("driver", cfg.Database.Driver),
			logger.Duration("elapsed", time.Since(stage)),
		)
	}
	// 2. 缓存：启用 Redis 时使用 Redis，否则回退到进程内缓存。
	if a.Cache == nil {
		stage := time.Now()
		c, err := cache.New(cfg.Redis)
		if err != nil {
			return fmt.Errorf("app: initialize cache: %w", err)
		}
		a.Cache = c
		backend := "memory"
		if cfg.Redis.Enabled {
			backend = "redis"
		}
		a.Logger.Info("cache initialized",
			logger.String("backend", backend),
			logger.Duration("elapsed", time.Since(stage)),
		)
	}

	// 3. 文件存储：文件是否公开由业务路由与授权决定，存储层只负责安全落盘。
	if a.Storage == nil {
		stage := time.Now()
		s, err := storage.New(cfg.Storage)
		if err != nil {
			return fmt.Errorf("app: initialize storage: %w", err)
		}
		a.Storage = s
		a.Logger.Info("storage initialized",
			logger.String("driver", cfg.Storage.Driver),
			logger.String("path", cfg.Storage.Path),
			logger.Duration("elapsed", time.Since(stage)),
		)
	}

	// 4. 定时任务调度器：只构造，不启动；由 Run 阶段统一启动。
	if a.Scheduler == nil {
		stage := time.Now()
		s, err := scheduler.New(a.Logger, cfg.Scheduler)
		if err != nil {
			return fmt.Errorf("app: initialize scheduler: %w", err)
		}
		a.Scheduler = s
		a.Logger.Debug("scheduler initialized",
			logger.Bool("enabled", cfg.Scheduler.Enabled),
			logger.Duration("elapsed", time.Since(stage)),
		)
	}

	// 5. 事件总线：进程内同步分发，业务可替换为可靠消息实现。
	if a.Events == nil {
		a.Events = event.New(a.Logger)
	}

	// 6. 后台协程池：用于审计落盘、通知、同步等异步任务，避免 goroutine 失控。
	if a.Pool == nil {
		a.Pool = pool.New(pool.Options{
			Name:      cfg.App.Name,
			Workers:   cfg.Pool.Workers,
			QueueSize: cfg.Pool.QueueSize,
			Logger:    a.Logger,
		})
		a.Logger.Debug("worker pool created",
			logger.Int("workers", a.Pool.Workers()),
			logger.Int("queue_size", cfg.Pool.QueueSize),
		)
	}

	// 7. 审计：未启用时使用空实现，保证调用方无需判空。
	if a.Auditor == nil {
		if cfg.Security.Audit.Enabled {
			a.Auditor = audit.NewLogAuditor(a.Logger, cfg.Security.Audit.MaxDetailSize.Bytes())
		} else {
			a.Auditor = audit.NopAuditor{}
		}
	}
	// 8. 认证：默认使用内存会话仓库，生产环境应通过 WithAuth 注入持久化实现。
	if a.Auth == nil && cfg.Security.Auth.Enabled {
		authService, err := auth.NewService(cfg.Security.Auth, auth.NewMemorySessionRepository(), a.Logger)
		if err != nil {
			return fmt.Errorf("app: initialize auth: %w", err)
		}
		a.Auth = authService
	}

	// 9. 授权：默认空策略 + 超级管理员角色，业务模块按需 Grant。
	if a.Permission == nil {
		a.Permission = permission.NewRBAC(permission.DefaultPolicy())
	}

	// 10. 限流与 CSRF：仅在安全配置启用时创建对应组件。
	if cfg.Security.RateLimit.Enabled {
		a.limiter = ratelimit.NewKeyedLimiter(cfg.Security.RateLimit.GlobalRate, cfg.Security.RateLimit.GlobalBurst)
	}
	if cfg.Security.CSRF.Enabled {
		protector, err := csrf.New(cfg.Security.CSRF)
		if err != nil {
			return fmt.Errorf("app: initialize csrf: %w", err)
		}
		a.csrf = protector
	}

	// 11. 健康检查：数据库与缓存是关键依赖，失败时 /health/ready 返回 503。
	if a.DB != nil {
		_ = a.Health.Add(health.DatabaseCheck("database", a.DB))
	}
	if pinger, ok := a.Cache.(cache.Pinger); ok {
		_ = a.Health.Add(health.CacheCheck("cache", pinger))
	}

	// 12. HTTP 引擎：先挂全局中间件，再创建 API 分组。
	// Gin 的分组会快照创建时的中间件链，顺序颠倒会导致业务路由绕过安全中间件。
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
		return fmt.Errorf("app: setup middleware: %w", err)
	}

	// 必须在注册全局中间件之后再创建 API 分组。
	a.Router = router.New(engine, cfg.Server)

	a.Health.Register(engine)
	engine.GET("/metrics", a.Metrics.Handler())

	// 13. 业务模块：按依赖拓扑初始化并注册路由。
	if err := a.Modules.Init(a); err != nil {
		return fmt.Errorf("app: initialize modules: %w", err)
	}
	if err := a.Modules.RegisterRoutes(a.Router.API()); err != nil {
		return fmt.Errorf("app: register module routes: %w", err)
	}
	if err := a.setupNoRoute(engine); err != nil {
		return fmt.Errorf("app: setup fallback routes: %w", err)
	}

	// 14. HTTP Server：仅构造，不监听；由 Run 阶段启动。
	srv, err := server.New(server.Options{
		Config:  cfg.Server,
		TLS:     cfg.TLS,
		Handler: engine,
		Logger:  a.Logger,
	})
	if err != nil {
		return fmt.Errorf("app: initialize http server: %w", err)
	}
	a.Server = srv

	a.Logger.Info("application initialized",
		logger.Bool("database", a.DB != nil),
		logger.Int("modules", len(a.Modules.List())),
		logger.Int("pool_workers", a.Pool.Workers()),
		logger.Duration("elapsed", time.Since(started)),
	)
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

	// 配置热更新：仅日志等级等少量配置支持热更新，监听失败不阻断启动。
	if a.loader != nil {
		watchCtx, cancel := context.WithCancel(runCtx)
		a.mu.Lock()
		a.watchCancel = cancel
		a.mu.Unlock()
		go func() {
			// 配置监听运行在独立 goroutine，任何 panic 都必须隔离，避免拖垮进程。
			defer func() {
				if recovered := recover(); recovered != nil {
					a.Logger.Error("config watcher panic",
						logger.Any("panic", recovered),
						logger.String("stack", string(debug.Stack())),
					)
				}
			}()
			if err := a.loader.Watch(watchCtx, a.onConfigChange); err != nil {
				a.Logger.Warn("config watch disabled", logger.Error(err))
			}
		}()
	}

	// 后台协程池先启动，供模块后台服务提交异步任务。
	// 这里不使用 runCtx：信号取消 runCtx 时池应立即停止接收新任务，
	// 但仍需在 Shutdown 中排空已入队任务，因此池生命周期由 Stop 统一管理。
	if a.Pool != nil {
		if err := a.Pool.Start(context.Background()); err != nil {
			_ = a.shutdown()
			return fmt.Errorf("app: start worker pool: %w", err)
		}
	}

	// 启动顺序：定时任务 -> 模块后台服务 -> HTTP Server。
	// 模块必须先完成 Start 才能对外提供服务，HTTP Server 最后监听端口。
	if a.Config.Scheduler.Enabled {
		if err := a.Scheduler.Start(runCtx); err != nil {
			_ = a.shutdown()
			return fmt.Errorf("app: start scheduler: %w", err)
		}
	}
	if err := a.Modules.Start(runCtx); err != nil {
		_ = a.shutdown()
		return fmt.Errorf("app: start modules: %w", err)
	}
	if err := a.Server.Start(); err != nil {
		_ = a.shutdown()
		return fmt.Errorf("app: start http server: %w", err)
	}

	// 所有依赖就绪后才标记 ready，避免流量打到尚未完成初始化的实例。
	a.Health.SetReady(true)
	a.Logger.Info("application started",
		logger.String("name", a.Config.App.Name),
		logger.String("version", a.Config.App.Version),
		logger.String("environment", a.Config.App.Environment),
		logger.String("addr", a.Server.Addr()),
	)

	serverErr := make(chan error, 1)
	go func() {
		// HTTP Serve 内部异常必须转换成错误返回，而不是让 panic 终止进程。
		defer func() {
			if recovered := recover(); recovered != nil {
				serverErr <- fmt.Errorf("app: http server panic: %v", recovered)
			}
		}()
		serverErr <- a.Server.Wait()
	}()

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

// Shutdown 优雅关闭应用：HTTP 服务 -> 模块 -> 定时任务 -> 协程池 -> 缓存 -> 数据库 -> 日志。
// 每个步骤都带超时控制；单个步骤失败不会阻断后续资源释放。
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.state == stateStopped || a.state == stateCreated {
		a.mu.Unlock()
		return nil
	}
	a.state = stateStopped
	watchCancel := a.watchCancel
	a.mu.Unlock()

	log := a.Logger
	if log == nil {
		log = logger.NewNop()
	}

	a.Health.SetReady(false)
	started := time.Now()
	log.Info("application shutdown started")

	var errs []error
	// step 统一处理单个关闭步骤的日志与错误聚合，保证一个步骤失败后继续释放其他资源。
	step := func(name string, fn func() error) {
		stage := time.Now()
		if err := fn(); err != nil {
			log.Warn("shutdown step failed",
				logger.String("step", name),
				logger.Error(err),
			)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return
		}
		log.Debug("shutdown step completed",
			logger.String("step", name),
			logger.Duration("elapsed", time.Since(stage)),
		)
	}

	if a.Server != nil {
		step("http server", func() error { return a.Server.Shutdown(ctx) })
	}
	if a.Modules != nil {
		step("modules", func() error { return a.Modules.Stop(ctx) })
	}
	if a.Scheduler != nil {
		step("scheduler", func() error { return a.Scheduler.Stop(ctx) })
	}
	// 协程池最后停止：模块停止阶段仍可能需要提交异步任务。
	if a.Pool != nil {
		step("worker pool", func() error { return a.Pool.Stop(ctx) })
	}
	if watchCancel != nil {
		watchCancel()
	}
	if a.Cache != nil {
		step("cache", a.Cache.Close)
	}
	if a.DB != nil {
		step("database", a.DB.Close)
	}

	log.Info("application stopped",
		logger.Duration("elapsed", time.Since(started)),
		logger.Int("errors", len(errs)),
	)
	if a.Logger != nil {
		if closer, ok := a.Logger.(interface{ Close() error }); ok {
			_ = closer.Close()
		} else {
			_ = a.Logger.Sync()
		}
	}
	return stderrors.Join(errs...)
}

// Submit 向应用后台协程池提交任务。
// 任务应尊重 ctx 取消信号，并在 Stop 超时前退出，否则只能依赖进程退出兜底。
func (a *App) Submit(ctx context.Context, task pool.Task) error {
	if a.Pool == nil {
		return pool.ErrNotStarted
	}
	return a.Pool.Submit(ctx, task)
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
