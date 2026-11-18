package lock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisPrefix = "goframe:lock:"
	defaultRetryDelay  = 50 * time.Millisecond
)

// unlockScript 仅删除值为当前 token 的 key，避免误删已过期后被他人获取的锁。
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`

// renewScript 仅在仍持锁时续期，避免把已丢失的锁续回来。
const renewScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`

// RedisOptions 是 Redis 分布式锁配置。
type RedisOptions struct {
	// Prefix 是 key 前缀，默认 "goframe:lock:"。
	Prefix string
	// AutoRenew 开启后按 ttl/3 自动续期，直到解锁。
	AutoRenew bool
	// RetryInterval 是 Lock 阻塞重试的间隔，默认 50ms。
	RetryInterval time.Duration
}

// DefaultRedisOptions 返回 Redis 锁默认配置。
func DefaultRedisOptions() RedisOptions {
	return RedisOptions{Prefix: defaultRedisPrefix, RetryInterval: defaultRetryDelay}
}

// RedisLocker 是基于 Redis 的分布式锁实现。
// 获取锁使用 SET NX PX + 随机 token，解锁使用 Lua compare-and-delete。
type RedisLocker struct {
	client        redis.UniversalClient
	prefix        string
	autoRenew     bool
	retryInterval time.Duration
}

// NewRedis 创建 Redis 分布式锁。client 为 nil 时调用方法会返回错误。
func NewRedis(client redis.UniversalClient, opts RedisOptions) *RedisLocker {
	defaults := DefaultRedisOptions()
	if opts.Prefix == "" {
		opts.Prefix = defaults.Prefix
	}
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = defaults.RetryInterval
	}
	return &RedisLocker{
		client:        client,
		prefix:        opts.Prefix,
		autoRenew:     opts.AutoRenew,
		retryInterval: opts.RetryInterval,
	}
}

var _ Locker = (*RedisLocker)(nil)

// TryLock 尝试获取 Redis 锁。ttl 必须大于 0。
func (l *RedisLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("lock: redis client is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("lock: key must not be empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("lock: ttl must be greater than 0")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fullKey := l.prefix + key
	token := newToken()
	ok, err := l.client.SetNX(ctx, fullKey, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("lock: acquire %q: %w", key, err)
	}
	if !ok {
		return nil, ErrLocked
	}
	return l.newUnlock(fullKey, token, ttl), nil
}

// Lock 获取 Redis 锁，直到成功或 ctx 结束。
func (l *RedisLocker) Lock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("lock: redis client is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("lock: key must not be empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("lock: ttl must be greater than 0")
	}

	for {
		unlock, err := l.TryLock(ctx, key, ttl)
		if err == nil {
			return unlock, nil
		}
		if err != ErrLocked {
			return nil, err
		}

		timer := time.NewTimer(l.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("lock: acquire %q: %w", key, ctx.Err())
		case <-timer.C:
		}
	}
}

// newUnlock 构造解锁函数，并在解锁时停止自动续期。
func (l *RedisLocker) newUnlock(fullKey, token string, ttl time.Duration) UnlockFunc {
	stop := make(chan struct{})
	done := make(chan struct{})

	if l.autoRenew {
		interval := ttl / 3
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		go l.renewLoop(fullKey, token, ttl, interval, stop, done)
	} else {
		close(done)
	}

	var (
		mu       sync.Mutex
		released bool
	)
	return func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return ErrNotHeld
		}
		released = true

		close(stop)
		<-done
		return l.release(ctx, fullKey, token)
	}
}

// renewLoop 周期性续期，直到解锁或发现锁已不再属于自己。
func (l *RedisLocker) renewLoop(fullKey, token string, ttl, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), interval)
			ok, err := l.renew(ctx, fullKey, token, ttl)
			cancel()
			if err != nil || !ok {
				// 续期失败通常意味着锁已过期或被他人持有，停止续期，避免误操作。
				return
			}
		}
	}
}

func (l *RedisLocker) renew(ctx context.Context, fullKey, token string, ttl time.Duration) (bool, error) {
	ms := ttl.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	n, err := l.client.Eval(ctx, renewScript, []string{fullKey}, token, ms).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (l *RedisLocker) release(ctx context.Context, fullKey, token string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	n, err := l.client.Eval(ctx, unlockScript, []string{fullKey}, token).Int64()
	if err != nil {
		return fmt.Errorf("lock: release %q: %w", fullKey, err)
	}
	if n == 0 {
		return ErrNotHeld
	}
	return nil
}
