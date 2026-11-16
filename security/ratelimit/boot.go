package ratelimit

import (
	"context"
	"fmt"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("security.rate_limit", DefaultConfig())
	global = struct {
		sync.RWMutex
		limiter Limiter
	}{}
)

// Current 返回当前限流配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局限流器；未启用或未启动时返回 nil。
func Get() Limiter {
	global.RLock()
	defer global.RUnlock()
	return global.limiter
}

func init() {
	_ = boot.Register("ratelimit", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 创建全局限流器。
func Startup(context.Context) error {
	current := Current()
	if !current.Enabled {
		return nil
	}
	if current.GlobalRate <= 0 || current.GlobalBurst <= 0 {
		return fmt.Errorf("ratelimit: global rate/burst must be greater than 0")
	}
	instance := NewKeyedLimiterWithLimit(current.GlobalRate, current.GlobalBurst, current.MaxBuckets)
	global.Lock()
	global.limiter = instance
	global.Unlock()
	return nil
}

// Shutdown 释放限流器。
func Shutdown(context.Context) error {
	global.Lock()
	instance := global.limiter
	global.limiter = nil
	global.Unlock()
	if instance != nil {
		if closer, ok := instance.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}
