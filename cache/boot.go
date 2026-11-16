package cache

import (
	"context"
	"fmt"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("redis", DefaultConfig())
	global = struct {
		sync.RWMutex
		cache Cache
	}{}
)

// Current 返回当前缓存配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局缓存实例；boot.Startup 前返回 nil。
func Get() Cache {
	global.RLock()
	defer global.RUnlock()
	return global.cache
}

func init() {
	_ = boot.Register("cache", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 创建缓存实例。
func Startup(context.Context) error {
	current := Current()
	if current.Enabled && current.Addr == "" {
		return fmt.Errorf("cache: redis addr is required when enabled")
	}

	instance, err := New(*current)
	if err != nil {
		return err
	}
	global.Lock()
	global.cache = instance
	global.Unlock()
	return nil
}

// Shutdown 关闭缓存连接。
func Shutdown(context.Context) error {
	global.Lock()
	instance := global.cache
	global.cache = nil
	global.Unlock()
	if instance != nil {
		return instance.Close()
	}
	return nil
}
