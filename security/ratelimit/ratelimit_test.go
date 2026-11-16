package ratelimit

import (
	"context"
	stderrors "errors"
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
