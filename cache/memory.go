package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const defaultMaxEntries = 100000

type memoryItem struct {
	data      []byte
	expiresAt time.Time
}

// MemoryOptions 是内存缓存配置。
type MemoryOptions struct {
	// MaxEntries 是 key 数量上限，<=0 时使用默认值 100000。
	MaxEntries int
}

// MemoryCache 是进程内缓存实现，适用于单实例部署与测试。
type MemoryCache struct {
	mu         sync.RWMutex
	items      map[string]memoryItem
	maxEntries int
	stop       chan struct{}
	closeOnce  sync.Once
}

// NewMemory 创建内存缓存，使用默认容量上限。
func NewMemory() *MemoryCache {
	return NewMemoryWithOptions(MemoryOptions{})
}

// NewMemoryWithOptions 创建带容量上限的内存缓存。
func NewMemoryWithOptions(opts MemoryOptions) *MemoryCache {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = defaultMaxEntries
	}
	c := &MemoryCache{
		items:      make(map[string]memoryItem),
		maxEntries: opts.MaxEntries,
		stop:       make(chan struct{}),
	}
	go c.janitor()
	return c
}

// Get 读取缓存并反序列化到 value（value 必须为指针）。
func (c *MemoryCache) Get(ctx context.Context, key string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return ErrMiss
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		_ = c.Delete(ctx, key)
		return ErrMiss
	}
	if err := json.Unmarshal(item.data, value); err != nil {
		return fmt.Errorf("cache: decode %q: %w", key, err)
	}
	return nil
}

// Set 写入缓存，ttl<=0 表示不过期。
func (c *MemoryCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: encode %q: %w", key, err)
	}
	item := memoryItem{data: data}
	if ttl > 0 {
		item.expiresAt = time.Now().Add(ttl)
	}

	c.mu.Lock()
	if _, exists := c.items[key]; !exists && c.maxEntries > 0 && len(c.items) >= c.maxEntries {
		c.evictLocked(time.Now())
	}
	c.items[key] = item
	c.mu.Unlock()
	return nil
}

// evictLocked 在容量达到上限时腾出位置：优先淘汰已过期项，否则淘汰任意项。
// 调用方必须持有 c.mu。
func (c *MemoryCache) evictLocked(now time.Time) {
	for key, item := range c.items {
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			delete(c.items, key)
			return
		}
	}
	for key := range c.items {
		delete(c.items, key)
		return
	}
}

// Delete 删除缓存。
func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

// Exists 判断缓存是否存在且未过期。
func (c *MemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		_ = c.Delete(ctx, key)
		return false, nil
	}
	return true, nil
}

// Ping 实现 Pinger，内存缓存始终可用。
func (c *MemoryCache) Ping(ctx context.Context) error { return ctx.Err() }

// Close 停止清理协程。
func (c *MemoryCache) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return nil
}

func (c *MemoryCache) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			c.mu.Lock()
			for key, item := range c.items {
				if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
					delete(c.items, key)
				}
			}
			c.mu.Unlock()
		}
	}
}
