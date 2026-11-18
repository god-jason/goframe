package captcha

import (
	"context"
	"sync"
	"time"
)

const memorySweepInterval = time.Minute

type memoryEntry struct {
	code      string
	expiresAt time.Time
}

// MemoryStore 是进程内验证码存储，适用于单实例部署与测试。
type MemoryStore struct {
	mu        sync.Mutex
	items     map[string]memoryEntry
	lastSweep time.Time
}

// NewMemoryStore 创建内存验证码存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items:     make(map[string]memoryEntry),
		lastSweep: time.Now(),
	}
}

// Set 写入验证码，ttl<=0 表示不过期。
func (s *MemoryStore) Set(ctx context.Context, id, code string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := memoryEntry{code: code}
	if ttl > 0 {
		entry.expiresAt = now.Add(ttl)
	}
	s.items[id] = entry
	s.sweepLocked(now)
	return nil
}

// Get 读取验证码。不存在的返回 ErrNotFound，已过期的返回 ErrExpired。
func (s *MemoryStore) Get(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.items[id]
	if !ok {
		s.sweepLocked(now)
		return "", ErrNotFound
	}
	if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		delete(s.items, id)
		return "", ErrExpired
	}
	s.sweepLocked(now)
	return entry.code, nil
}

// Delete 删除验证码。
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < memorySweepInterval {
		return
	}
	for key, entry := range s.items {
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			delete(s.items, key)
		}
	}
	s.lastSweep = now
}
