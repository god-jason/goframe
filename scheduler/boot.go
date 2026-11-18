package scheduler

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("scheduler", DefaultConfig())
	global = struct {
		scheduler Scheduler
	}{}
)

// GetConfig 返回当前定时任务配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局调度器；未启用或未启动时返回 nil。
func Get() Scheduler {
	return global.scheduler
}

func init() {
	_ = boot.Register("scheduler", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建并启动调度器。
func Startup(ctx context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if current.JobTimeout <= 0 {
		return fmt.Errorf("scheduler: job_timeout must be greater than 0")
	}

	instance, err := New(logger.Get(), *current)
	if err != nil {
		return err
	}
	if err := instance.Start(ctx); err != nil {
		return err
	}
	global.scheduler = instance
	return nil
}

// Shutdown 停止调度器。
func Shutdown(ctx context.Context) error {
	instance := global.scheduler
	global.scheduler = nil
	if instance != nil {
		return instance.Stop(ctx)
	}
	return nil
}
