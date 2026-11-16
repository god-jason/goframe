package database

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("database", DefaultConfig())
	global = struct {
		sync.RWMutex
		db Database
	}{}
)

// Current 返回当前数据库配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局数据库实例；未启用或未启动时返回 nil。
func Get() Database {
	global.RLock()
	defer global.RUnlock()
	return global.db
}

func init() {
	_ = boot.Register("database", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 根据配置建立数据库连接。
func Startup(ctx context.Context) error {
	current := Current()
	if !current.Enabled() {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	db, err := Open(*current, logger.Get())
	if err != nil {
		return err
	}
	if err := db.Ping(ctx); err != nil {
		_ = db.Close()
		return err
	}

	global.Lock()
	global.db = db
	global.Unlock()
	return nil
}

// Shutdown 关闭数据库连接。
func Shutdown(context.Context) error {
	global.Lock()
	db := global.db
	global.db = nil
	global.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}
