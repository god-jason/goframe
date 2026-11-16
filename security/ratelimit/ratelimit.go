// Package ratelimit 提供令牌桶限流能力。
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// shardCount 是分片数量，降低高并发下的全局锁竞争。
	shardCount = 16
	// defaultMaxBuckets 是默认的 key 数量上限，防止高基数 key 持续占用内存。
	defaultMaxBuckets = 100000
	// cleanupInterval 是空闲桶清理间隔。
	cleanupInterval = time.Minute
)

// Limiter 是框架统一限流接口。key 可以是 IP、用户、租户或接口维度。
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// KeyedLimiter 为每个 key 维护独立令牌桶，并定期回收空闲桶。
// 内部按 key 分片，避免所有请求竞争同一把锁。
type KeyedLimiter struct {
	limit      rate.Limit
	burst      int
	ttl        time.Duration
	maxBuckets int
	shards     [shardCount]limiterShard
}

type limiterShard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewKeyedLimiter 创建按 key 限流器。perSecond 为每秒补充的令牌数，burst 为桶容量。
func NewKeyedLimiter(perSecond float64, burst int) *KeyedLimiter {
	return NewKeyedLimiterWithLimit(perSecond, burst, defaultMaxBuckets)
}

// NewKeyedLimiterWithLimit 创建带 key 数量上限的限流器。
// maxBuckets<=0 时使用默认值，避免被伪造 IP/租户等高基数 key 打爆内存。
func NewKeyedLimiterWithLimit(perSecond float64, burst, maxBuckets int) *KeyedLimiter {
	if perSecond <= 0 {
		perSecond = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if maxBuckets <= 0 {
		maxBuckets = defaultMaxBuckets
	}
	l := &KeyedLimiter{
		limit:      rate.Limit(perSecond),
		burst:      burst,
		ttl:        10 * time.Minute,
		maxBuckets: maxBuckets,
	}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
		l.shards[i].lastGC = time.Now()
	}
	return l
}

// Allow 判断请求是否被允许。
func (l *KeyedLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	shard := l.shardFor(key)
	now := time.Now()

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if now.Sub(shard.lastGC) > cleanupInterval {
		for bucketKey, b := range shard.buckets {
			if now.Sub(b.lastSeen) > l.ttl {
				delete(shard.buckets, bucketKey)
			}
		}
		shard.lastGC = now
	}

	b, ok := shard.buckets[key]
	if !ok {
		maxPerShard := l.maxBuckets/shardCount + 1
		if len(shard.buckets) >= maxPerShard {
			l.evictLocked(shard, now)
		}
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		shard.buckets[key] = b
	}
	b.lastSeen = now
	return b.limiter.Allow(), nil
}

// evictLocked 在分片容量达到上限时腾出一个位置：优先淘汰过期桶，否则淘汰任意桶。
// 调用方必须持有 shard.mu。
func (l *KeyedLimiter) evictLocked(shard *limiterShard, now time.Time) {
	for key, b := range shard.buckets {
		if now.Sub(b.lastSeen) > l.ttl {
			delete(shard.buckets, key)
			return
		}
	}
	for key := range shard.buckets {
		delete(shard.buckets, key)
		return
	}
}

// shardFor 使用 FNV-1a 选择分片，避免额外内存分配。
func (l *KeyedLimiter) shardFor(key string) *limiterShard {
	hash := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= 16777619
	}
	return &l.shards[hash%shardCount]
}

// Close 清空所有令牌桶。
func (l *KeyedLimiter) Close() error {
	for i := range l.shards {
		shard := &l.shards[i]
		shard.mu.Lock()
		shard.buckets = make(map[string]*bucket)
		shard.lastGC = time.Now()
		shard.mu.Unlock()
	}
	return nil
}
