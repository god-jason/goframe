// Package cache 定义统一缓存接口，提供内存与 Redis 实现。
package cache

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/god-jason/goframe/config"
)

// ErrMiss 表示缓存未命中。
var ErrMiss = stderrors.New("cache: key not found")

// Cache 是框架统一缓存接口。缓存只用于提升性能，不作为业务数据的唯一来源。
type Cache interface {
	Get(ctx context.Context, key string, value any) error
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Close() error
}

// Pinger 由支持连通性检查的缓存实现（用于健康检查）。
type Pinger interface {
	Ping(ctx context.Context) error
}

// New 根据配置创建缓存实例：启用 Redis 时使用 Redis，否则使用进程内内存缓存。
func New(cfg config.RedisConfig) (Cache, error) {
	if cfg.Enabled {
		return NewRedis(cfg)
	}
	return NewMemory(), nil
}
