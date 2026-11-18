package lock

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// maxRetryDelay 是 Lock 轮询的最大间隔，避免长时间占用 CPU。
const maxRetryDelay = 50 * time.Millisecond

// MemoryLocker 是进程内锁实现，支持过期自动释放。
// 它只在本进程内有效，多实例部署请使用 RedisLocker。
type MemoryLocker struct {
	mu      sync.Mutex
	entries map[string]*memoryEntry
}

type memoryEntry struct {
	token     string
	expiresAt time.Time // 零值表示永不过期
}

// NewMemory 创建进程内锁。
func NewMemory() *MemoryLocker {
	return &MemoryLocker{entries: make(map[string]*memoryEntry)}
}

var _ Locker = (*MemoryLocker)(nil)

// TryLock 尝试获取进程内锁。ttl<=0 表示永不过期。
func (l *MemoryLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error) {
	if key == "" {
		return nil, fmt.Errorf("lock: key must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	token := newToken()
	now := time.Now()

	l.mu.Lock()
	if entry, ok := l.entries[key]; ok {
		if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
			l.mu.Unlock()
			return nil, ErrLocked
		}
		// 已过期，允许新持有者接管。
	}
	entry := &memoryEntry{token: token}
	if ttl > 0 {
		entry.expiresAt = now.Add(ttl)
	}
	l.entries[key] = entry
	l.mu.Unlock()

	var (
		mu       sync.Mutex
		released bool
	)
	return func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return ErrNotHeld
		}
		released = true

		l.mu.Lock()
		defer l.mu.Unlock()
		current, ok := l.entries[key]
		if !ok || current.token != token {
			return ErrNotHeld
		}
		delete(l.entries, key)
		return nil
	}, nil
}

// Lock 获取进程内锁，直到成功或 ctx 结束。
// 采用指数退避轮询，兼顾响应速度与 CPU 占用。
func (l *MemoryLocker) Lock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error) {
	if key == "" {
		return nil, fmt.Errorf("lock: key must not be empty")
	}

	delay := time.Millisecond
	for {
		unlock, err := l.TryLock(ctx, key, ttl)
		if err == nil {
			return unlock, nil
		}
		if err != ErrLocked {
			return nil, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("lock: acquire %q: %w", key, ctx.Err())
		case <-timer.C:
		}
		delay *= 2
		if delay > maxRetryDelay {
			delay = maxRetryDelay
		}
	}
}
