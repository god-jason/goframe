package auth

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("security.auth", DefaultConfig())
	global = struct {
		sync.RWMutex
		manager    Manager
		repository SessionRepository
	}{}
)

// SetSessionRepository 注入会话仓库，必须在 boot.Startup 前调用。
// 生产环境应使用数据库实现替换默认内存仓库。
func SetSessionRepository(repo SessionRepository) {
	global.Lock()
	global.repository = repo
	global.Unlock()
}

// Current 返回当前认证配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局认证管理器；未启用或未启动时返回 nil。
func Get() Manager {
	global.RLock()
	defer global.RUnlock()
	return global.manager
}

func init() {
	_ = boot.Register("auth", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建认证服务。默认使用内存会话仓库，生产环境应替换为持久化实现。
func Startup(context.Context) error {
	current := Current()
	if !current.Enabled {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	global.RLock()
	repository := global.repository
	global.RUnlock()
	if repository == nil {
		repository = NewMemorySessionRepository()
	}

	service, err := NewService(*current, repository, logger.Get())
	if err != nil {
		return err
	}
	global.Lock()
	global.manager = service
	global.Unlock()
	return nil
}

// Shutdown 释放认证服务。
func Shutdown(context.Context) error {
	global.Lock()
	global.manager = nil
	global.Unlock()
	return nil
}
