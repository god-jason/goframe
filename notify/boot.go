package notify

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/taskqueue"
)

var global = struct {
	mu     sync.RWMutex
	sender Sender
}{}

func init() {
	taskqueue.Register(TaskType, handleQueueTask)
	_ = boot.Register("notify", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "taskqueue"},
	})
}

// GetSender 返回全局发送器；未启用或未启动时返回 nil。
func GetSender() Sender {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.sender
}

// SetSender 注入全局发送器，覆盖配置文件构建的实例；传 nil 清空。
func SetSender(s Sender) {
	global.mu.Lock()
	global.sender = s
	global.mu.Unlock()
}

// Startup 按配置构建全局发送器。未启用时不构建，Send 保持报错。
func Startup(context.Context) error {
	current := GetConfig().normalize()
	if !current.Enabled {
		return nil
	}
	sender, err := current.buildSender()
	if err != nil {
		return err
	}
	SetSender(sender)
	logger.Get().Info("notify channel loaded", logger.String("channel", current.Channel))
	return nil
}

// Shutdown 清空全局发送器。
func Shutdown(context.Context) error {
	SetSender(nil)
	return nil
}
