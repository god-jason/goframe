package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestNewKeyedLimiterDefaults(t *testing.T) {
	l := NewKeyedLimiterWithLimit(0, 0, 0)
	defer l.Close()

	if l.limit != 1 {
		t.Fatalf("limit = %v, want 1", l.limit)
	}
	if l.burst != 1 {
		t.Fatalf("burst = %d, want 1", l.burst)
	}
	if l.maxBuckets != defaultMaxBuckets {
		t.Fatalf("maxBuckets = %d, want %d", l.maxBuckets, defaultMaxBuckets)
	}
	if l.ttl != 10*time.Minute {
		t.Fatalf("ttl = %v", l.ttl)
	}
}

func TestKeyedLimiterGCExpiredBuckets(t *testing.T) {
	l := NewKeyedLimiterWithLimit(1, 1, 16)
	defer l.Close()
	ctx := context.Background()

	if _, err := l.Allow(ctx, "stale"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	shard := l.shardFor("stale")

	// 找到与 stale 同分片的另一个 key。
	other := ""
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("other-%d", i)
		if l.shardFor(candidate) == shard {
			other = candidate
			break
		}
	}

	shard.mu.Lock()
	for _, b := range shard.buckets {
		b.lastSeen = time.Now().Add(-l.ttl - time.Minute)
	}
	shard.lastGC = time.Now().Add(-2 * cleanupInterval)
	shard.mu.Unlock()

	if _, err := l.Allow(ctx, other); err != nil {
		t.Fatalf("Allow other: %v", err)
	}

	shard.mu.Lock()
	_, staleExists := shard.buckets["stale"]
	shard.mu.Unlock()
	if staleExists {
		t.Fatal("expected stale bucket to be garbage collected")
	}
}

func TestKeyedLimiterEvictLocked(t *testing.T) {
	l := NewKeyedLimiterWithLimit(1, 1, 16)
	defer l.Close()

	shard := l.shardFor("evict")
	now := time.Now()

	shard.mu.Lock()
	shard.buckets["expired"] = &bucket{
		limiter:  rate.NewLimiter(1, 1),
		lastSeen: now.Add(-l.ttl - time.Minute),
	}
	l.evictLocked(shard, now)
	shard.mu.Unlock()

	shard.mu.Lock()
	if len(shard.buckets) != 0 {
		shard.mu.Unlock()
		t.Fatal("expected expired bucket to be evicted")
	}
	shard.buckets["live"] = &bucket{limiter: rate.NewLimiter(1, 1), lastSeen: now}
	l.evictLocked(shard, now)
	size := len(shard.buckets)
	shard.mu.Unlock()

	if size != 0 {
		t.Fatalf("expected arbitrary bucket to be evicted, size = %d", size)
	}
}

func TestKeyedLimiterEvictsWhenFull(t *testing.T) {
	l := NewKeyedLimiterWithLimit(1, 1, 1)
	defer l.Close()
	ctx := context.Background()

	if _, err := l.Allow(ctx, "first"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	shard := l.shardFor("first")
	other := ""
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("full-%d", i)
		if l.shardFor(candidate) == shard {
			other = candidate
			break
		}
	}
	if _, err := l.Allow(ctx, other); err != nil {
		t.Fatalf("Allow other: %v", err)
	}

	shard.mu.Lock()
	size := len(shard.buckets)
	shard.mu.Unlock()
	if size != 1 {
		t.Fatalf("bucket count = %d, want 1", size)
	}
}

func TestKeyedLimiterHighConcurrency(t *testing.T) {
	l := NewKeyedLimiterWithLimit(1000, 1000, 1000)
	defer l.Close()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_, _ = l.Allow(context.Background(), fmt.Sprintf("worker:%d:%d", worker, j))
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
