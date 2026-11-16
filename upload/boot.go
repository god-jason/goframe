package upload

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/storage"
)

var (
	cfg    = config.Register("security.upload", DefaultConfig())
	global = struct {
		sync.RWMutex
		service *Service
		scanner Scanner
	}{}
)

// SetScanner 注入上传文件安全扫描器，必须在 boot.Startup 前调用。
func SetScanner(scanner Scanner) {
	global.Lock()
	global.scanner = scanner
	global.Unlock()
}

// Current 返回当前上传配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局上传服务；boot.Startup 前返回 nil。
func Get() *Service {
	global.RLock()
	defer global.RUnlock()
	return global.service
}

func init() {
	_ = boot.Register("upload", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "storage"},
	})
}

// Startup 创建全局上传服务。
func Startup(context.Context) error {
	store := storage.Get()
	if store == nil {
		return nil
	}
	global.RLock()
	scanner := global.scanner
	global.RUnlock()

	var options []Option
	if scanner != nil {
		options = append(options, WithScanner(scanner))
	}
	service, err := New(store, *Current(), logger.Get(), options...)
	if err != nil {
		return err
	}
	global.Lock()
	global.service = service
	global.Unlock()
	return nil
}

// Shutdown 释放上传服务。
func Shutdown(context.Context) error {
	global.Lock()
	global.service = nil
	global.Unlock()
	return nil
}
