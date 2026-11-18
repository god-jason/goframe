package event

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
)

var global = struct {
	bus EventBus
}{}

// Get 返回全局事件总线；boot.Startup 前返回 nil。
func Get() EventBus {
	return global.bus
}

func init() {
	_ = boot.Register("event", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"logger"},
	})
}

// Startup 创建全局事件总线。
func Startup(context.Context) error {
	global.bus = New(logger.Get())
	return nil
}

// Shutdown 释放事件总线。
func Shutdown(context.Context) error {
	global.bus = nil
	return nil
}
