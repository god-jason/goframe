package app

import (
	"io/fs"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/database"
	"github.com/god-jason/goframe/event"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/permission"
	"github.com/god-jason/goframe/scheduler"
	"github.com/god-jason/goframe/security/audit"
	"github.com/god-jason/goframe/storage"
)

// Option 是应用构造选项，用于显式注入依赖。
type Option func(*App)

// WithConfig 注入已加载的配置。
func WithConfig(cfg *config.Config) Option {
	return func(a *App) { a.Config = cfg }
}

// WithConfigFile 指定配置文件路径。
func WithConfigFile(path string) Option {
	return func(a *App) { a.configFile = path }
}

// WithLogger 注入日志实现。
func WithLogger(log logger.Logger) Option {
	return func(a *App) { a.Logger = log }
}

// WithDatabase 注入数据库实现。
func WithDatabase(db database.Database) Option {
	return func(a *App) { a.DB = db }
}

// WithCache 注入缓存实现。
func WithCache(c cache.Cache) Option {
	return func(a *App) { a.Cache = c }
}

// WithStorage 注入文件存储实现。
func WithStorage(s storage.Storage) Option {
	return func(a *App) { a.Storage = s }
}

// WithAuth 注入认证管理实现。
func WithAuth(manager auth.Manager) Option {
	return func(a *App) { a.Auth = manager }
}

// WithPermission 注入授权实现。
func WithPermission(authorizer permission.Authorizer) Option {
	return func(a *App) { a.Permission = authorizer }
}

// WithScheduler 注入定时任务实现。
func WithScheduler(s scheduler.Scheduler) Option {
	return func(a *App) { a.Scheduler = s }
}

// WithEventBus 注入事件总线实现。
func WithEventBus(bus event.EventBus) Option {
	return func(a *App) { a.Events = bus }
}

// WithAuditor 注入审计实现。
func WithAuditor(auditor audit.Auditor) Option {
	return func(a *App) { a.Auditor = auditor }
}

// WithFileSystem 注入 SPA 静态资源文件系统（embed 模式）。
func WithFileSystem(fsys fs.FS) Option {
	return func(a *App) { a.fileSystem = fsys }
}
