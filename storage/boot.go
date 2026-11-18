package storage

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("storage", DefaultConfig())
	global = struct {
		store Storage
	}{}
)

// GetConfig 返回当前存储配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局存储实例；boot.Startup 前返回 nil。
func Get() Storage {
	return global.store
}

func init() {
	_ = boot.Register("storage", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 创建存储实例。
func Startup(context.Context) error {
	instance, err := New(*GetConfig())
	if err != nil {
		return err
	}
	global.store = instance
	return nil
}

// Shutdown 释放存储资源。当前本地存储无需关闭。
func Shutdown(context.Context) error {
	global.store = nil
	return nil
}
