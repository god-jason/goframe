package secrets

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RotatingProvider 在内存中保存当前密钥与上一代密钥。
// 轮换后旧密钥在 grace 期间仍然有效，供存量凭证平滑过渡。
type RotatingProvider struct {
	mu       sync.RWMutex
	grace    time.Duration
	current  map[string]string
	previous map[string]previousSecret
	now      func() time.Time
}

type previousSecret struct {
	value     string
	expiresAt time.Time
}

// NewRotating 创建支持轮换的密钥提供者。grace<=0 时使用 1 小时。
func NewRotating(grace time.Duration) *RotatingProvider {
	if grace <= 0 {
		grace = time.Hour
	}
	return &RotatingProvider{
		grace:    grace,
		current:  make(map[string]string),
		previous: make(map[string]previousSecret),
		now:      time.Now,
	}
}

// Set 写入当前密钥，不保留旧值。
func (p *RotatingProvider) Set(name, value string) {
	p.mu.Lock()
	p.current[name] = value
	delete(p.previous, name)
	p.mu.Unlock()
}

// Get 读取当前密钥。
func (p *RotatingProvider) Get(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.RLock()
	value, ok := p.current[name]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("secrets: %s is not configured", name)
	}
	return value, nil
}

// Rotate 使用生成器产生新密钥，旧密钥进入宽限期。
func (p *RotatingProvider) Rotate(ctx context.Context, name string, generator func(context.Context) (string, error)) (string, error) {
	if generator == nil {
		return "", fmt.Errorf("secrets: generator is nil")
	}
	value, err := generator(ctx)
	if err != nil {
		return "", fmt.Errorf("secrets: rotate %s: %w", name, err)
	}
	if value == "" {
		return "", fmt.Errorf("secrets: rotate %s: empty value", name)
	}

	p.mu.Lock()
	old, exists := p.current[name]
	now := p.now()
	p.current[name] = value
	if exists {
		p.previous[name] = previousSecret{value: old, expiresAt: now.Add(p.grace)}
	}
	p.cleanupLocked(now)
	p.mu.Unlock()
	return value, nil
}

// Previous 返回仍在宽限期内的旧密钥。
func (p *RotatingProvider) Previous(ctx context.Context, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	entry, ok := p.previous[name]
	if !ok || !p.now().Before(entry.expiresAt) {
		return nil, nil
	}
	return []string{entry.value}, nil
}

// Verify 判断候选值是否为当前密钥或宽限期内的旧密钥。
func (p *RotatingProvider) Verify(name, candidate string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if current, ok := p.current[name]; ok && current == candidate {
		return true
	}
	entry, ok := p.previous[name]
	return ok && p.now().Before(entry.expiresAt) && entry.value == candidate
}

// Cleanup 清理已过宽限期的旧密钥。
func (p *RotatingProvider) Cleanup() {
	p.mu.Lock()
	p.cleanupLocked(p.now())
	p.mu.Unlock()
}

func (p *RotatingProvider) cleanupLocked(now time.Time) {
	for name, entry := range p.previous {
		if !now.Before(entry.expiresAt) {
			delete(p.previous, name)
		}
	}
}
