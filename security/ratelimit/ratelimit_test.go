package ratelimit

import (
	"context"
	stderrors "errors"
	"strconv"
	"testing"
)

func TestKeyedLimiterBurst(t *testing.T) {
	limiter := NewKeyedLimiter(0.001, 2)
	defer limiter.Close()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(ctx, "ip:1.2.3.4")
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed=%v err=%v", i, allowed, err)
		}
	}
	allowed, err := limiter.Allow(ctx, "ip:1.2.3.4")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if allowed {
		t.Fatal("expected third request to be limited")
	}

	allowed, err = limiter.Allow(ctx, "ip:5.6.7.8")
	if err != nil || !allowed {
		t.Fatalf("expected other key to be independent: %v, %v", allowed, err)
	}
}

func TestKeyedLimiterBucketCap(t *testing.T) {
	limiter := NewKeyedLimiterWithLimit(1, 1, 32)
	defer limiter.Close()
	ctx := context.Background()

	for i := 0; i < 2000; i++ {
		key := "ip:10.0.0." + strconv.Itoa(i%255) + ":" + strconv.Itoa(i)
		if _, err := limiter.Allow(ctx, key); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}

	total := 0
	for i := range limiter.shards {
		limiter.shards[i].mu.Lock()
		total += len(limiter.shards[i].buckets)
		limiter.shards[i].mu.Unlock()
	}
	// 每个分片上限为 maxBuckets/shardCount+1，总量不超过 (32/16+1)*16。
	if total > 48 {
		t.Fatalf("bucket count = %d, want <= 48", total)
	}
}

func TestKeyedLimiterCanceledContext(t *testing.T) {
	limiter := NewKeyedLimiter(1, 1)
	defer limiter.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := limiter.Allow(ctx, "k"); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestKeyedLimiterCloseResets(t *testing.T) {
	limiter := NewKeyedLimiter(0.001, 1)
	ctx := context.Background()

	if allowed, _ := limiter.Allow(ctx, "k"); !allowed {
		t.Fatal("expected first request to pass")
	}
	if allowed, _ := limiter.Allow(ctx, "k"); allowed {
		t.Fatal("expected second request to be limited")
	}
	if err := limiter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if allowed, _ := limiter.Allow(ctx, "k"); !allowed {
		t.Fatal("expected limiter state to be reset after Close")
	}
}
