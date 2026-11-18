package ratelimit

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("security.rate_limit", DefaultConfig())
	global = struct {
		limiter Limiter
	}{}
)

// GetConfig 返回当前限流配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局限流器；未启用或未启动时返回 nil。
func Get() Limiter {
	return global.limiter
}

func init() {
	_ = boot.Register("ratelimit", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "cache"},
	})
}

// Startup 创建全局限流器。启用 Redis 时使用分布式限流，否则使用进程内令牌桶。
func Startup(context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if current.GlobalRate <= 0 || current.GlobalBurst <= 0 {
		return fmt.Errorf("ratelimit: global rate/burst must be greater than 0")
	}

	var instance Limiter
	if redisCache, ok := cache.Get().(*cache.RedisCache); ok && redisCache != nil {
		instance = NewRedisLimiter(redisCache.Client(), current.GlobalRate, current.GlobalBurst, "", current.FailOpen)
	} else {
		instance = NewKeyedLimiterWithLimit(current.GlobalRate, current.GlobalBurst, current.MaxBuckets)
	}
	global.limiter = instance
	return nil
}

// Shutdown 释放限流器。
func Shutdown(context.Context) error {
	instance := global.limiter
	global.limiter = nil
	if instance != nil {
		if closer, ok := instance.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}
