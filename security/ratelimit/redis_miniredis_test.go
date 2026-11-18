package ratelimit

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newMiniRedisLimiter(t *testing.T, limit float64, burst int, prefix string, failOpen bool) (*RedisLimiter, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisLimiter(client, limit, burst, prefix, failOpen), client, server
}

func TestRedisLimiterAllowAndDeny(t *testing.T) {
	limiter, _, _ := newMiniRedisLimiter(t, 0.001, 2, "test:", false)
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
		t.Fatalf("other key: allowed=%v err=%v", allowed, err)
	}
}

func TestRedisLimiterCanceledContextWithClient(t *testing.T) {
	limiter, _, _ := newMiniRedisLimiter(t, 1, 1, "", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := limiter.Allow(ctx, "k"); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Allow = %v, want context.Canceled", err)
	}
}

func TestRedisLimiterEmptyKey(t *testing.T) {
	limiter, _, _ := newMiniRedisLimiter(t, 1, 1, "", false)

	if _, err := limiter.Allow(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestRedisLimiterNilReceiver(t *testing.T) {
	var limiter *RedisLimiter
	if allowed, err := limiter.Allow(context.Background(), "k"); err == nil || allowed {
		t.Fatalf("Allow = %v, %v; want false, error", allowed, err)
	}
}

func TestRedisLimiterFailOpen(t *testing.T) {
	limiter, client, _ := newMiniRedisLimiter(t, 1, 1, "", true)
	_ = client.Close()

	allowed, err := limiter.Allow(context.Background(), "k")
	if err != nil || !allowed {
		t.Fatalf("failOpen Allow = %v, %v; want true, nil", allowed, err)
	}
}

func TestRedisLimiterFailClosed(t *testing.T) {
	limiter, client, _ := newMiniRedisLimiter(t, 1, 1, "", false)
	_ = client.Close()

	if allowed, err := limiter.Allow(context.Background(), "k"); err == nil || allowed {
		t.Fatalf("failClosed Allow = %v, %v; want false, error", allowed, err)
	}
}
