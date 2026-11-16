package event

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
)

var global = struct {
	sync.RWMutex
	bus EventBus
}{}

// Get 返回全局事件总线；boot.Startup 前返回 nil。
func Get() EventBus {
	global.RLock()
	defer global.RUnlock()
	return global.bus
}

func init() {
	_ = boot.Register("event", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"logger"},
	})
}

// Startup 创建全局事件总线。
func Startup(context.Context) error {
	global.Lock()
	global.bus = New(logger.Get())
	global.Unlock()
	return nil
}

// Shutdown 释放事件总线。
func Shutdown(context.Context) error {
	global.Lock()
	global.bus = nil
	global.Unlock()
	return nil
}
