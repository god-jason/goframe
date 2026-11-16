package storage

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("storage", DefaultConfig())
	global = struct {
		sync.RWMutex
		store Storage
	}{}
)

// Current 返回当前存储配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局存储实例；boot.Startup 前返回 nil。
func Get() Storage {
	global.RLock()
	defer global.RUnlock()
	return global.store
}

func init() {
	_ = boot.Register("storage", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 创建存储实例。
func Startup(context.Context) error {
	instance, err := New(*Current())
	if err != nil {
		return err
	}
	global.Lock()
	global.store = instance
	global.Unlock()
	return nil
}

// Shutdown 释放存储资源。当前本地存储无需关闭。
func Shutdown(context.Context) error {
	global.Lock()
	global.store = nil
	global.Unlock()
	return nil
}
