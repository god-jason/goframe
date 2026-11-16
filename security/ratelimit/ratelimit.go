// Package ratelimit 提供令牌桶限流能力。
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter 是框架统一限流接口。key 可以是 IP、用户、租户或接口维度。
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// KeyedLimiter 为每个 key 维护独立令牌桶，并定期回收空闲桶。
type KeyedLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	ttl     time.Duration
	lastGC  time.Time
	buckets map[string]*bucket
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewKeyedLimiter 创建按 key 限流器。perSecond 为每秒补充的令牌数，burst 为桶容量。
func NewKeyedLimiter(perSecond float64, burst int) *KeyedLimiter {
	if perSecond <= 0 {
		perSecond = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &KeyedLimiter{
		limit:   rate.Limit(perSecond),
		burst:   burst,
		ttl:     10 * time.Minute,
		lastGC:  time.Now(),
		buckets: make(map[string]*bucket),
	}
}

// Allow 判断请求是否被允许。
func (l *KeyedLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > 5*time.Minute {
		for bucketKey, b := range l.buckets {
			if now.Sub(b.lastSeen) > l.ttl {
				delete(l.buckets, bucketKey)
			}
		}
		l.lastGC = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = now
	return b.limiter.Allow(), nil
}

// Close 清空所有令牌桶。
func (l *KeyedLimiter) Close() error {
	l.mu.Lock()
	l.buckets = make(map[string]*bucket)
	l.mu.Unlock()
	return nil
}
