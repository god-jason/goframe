package auth

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("security.auth", DefaultConfig())
	global = struct {
		manager    Manager
		repository SessionRepository
		apiKeys    APIKeyRepository
	}{}
)

// SetSessionRepository 注入会话仓库，必须在 boot.Startup 前调用。
// 生产环境应使用数据库实现替换默认内存仓库。
func SetSessionRepository(repo SessionRepository) {
	global.repository = repo
}

// SetAPIKeyRepository 注入 API Key 仓库，必须在认证请求开始前调用。
func SetAPIKeyRepository(repo APIKeyRepository) {
	global.apiKeys = repo
}

// GetConfig 返回当前认证配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局认证管理器；未启用或未启动时返回 nil。
func Get() Manager {
	return global.manager
}

func init() {
	_ = boot.Register("auth", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建认证服务。默认使用内存会话仓库，生产环境应替换为持久化实现。
func Startup(context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	repository := global.repository
	if repository == nil {
		repository = NewMemorySessionRepository()
	}

	service, err := NewService(*current, repository, logger.Get())
	if err != nil {
		return err
	}
	global.manager = service
	return nil
}

// Shutdown 释放认证服务。
func Shutdown(context.Context) error {
	global.manager = nil
	return nil
}
