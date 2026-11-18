package pool

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("pool", DefaultConfig())
	global = struct {
		pool *Pool
	}{}
)

// GetConfig 返回当前协程池配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局协程池；boot.Startup 前返回 nil。
func Get() *Pool {
	return global.pool
}

func init() {
	_ = boot.Register("pool", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建并启动协程池。
func Startup(context.Context) error {
	current := GetConfig()
	instance := New(Options{
		Name:      config.App().Name,
		Workers:   current.Workers,
		QueueSize: current.QueueSize,
		Logger:    logger.Get(),
	})
	// 协程池使用独立 context，应用退出时仍允许业务提交收尾任务，
	// 最终由 boot.Shutdown 排空队列。
	if err := instance.Start(context.Background()); err != nil {
		return err
	}
	global.pool = instance
	return nil
}

// Shutdown 停止协程池。
func Shutdown(ctx context.Context) error {
	instance := global.pool
	global.pool = nil
	if instance != nil {
		return instance.Stop(ctx)
	}
	return nil
}
