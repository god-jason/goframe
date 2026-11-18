package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewRedisLimiterDefaults(t *testing.T) {
	limiter := NewRedisLimiter(nil, 0, 0, "", false)
	if limiter.limit != 1 {
		t.Fatalf("limit = %v, want 1", limiter.limit)
	}
	if limiter.burst != 1 {
		t.Fatalf("burst = %d, want 1", limiter.burst)
	}
	if limiter.prefix != defaultRedisPrefix {
		t.Fatalf("prefix = %q, want %q", limiter.prefix, defaultRedisPrefix)
	}
	if got := limiter.key("ip:1.2.3.4"); got != defaultRedisPrefix+"ip:1.2.3.4" {
		t.Fatalf("key = %q", got)
	}
}

func TestNewRedisLimiterPrefix(t *testing.T) {
	limiter := NewRedisLimiter(nil, 100, 200, "custom", false)
	if limiter.prefix != "custom:" {
		t.Fatalf("prefix = %q, want custom:", limiter.prefix)
	}
	if got := limiter.key("user:1"); got != "custom:user:1" {
		t.Fatalf("key = %q, want custom:user:1", got)
	}

	limiter = NewRedisLimiter(nil, 100, 200, "custom:", false)
	if got := limiter.key("user:1"); got != "custom:user:1" {
		t.Fatalf("key with trailing colon = %q", got)
	}
}

func TestRedisLimiterNilClient(t *testing.T) {
	limiter := NewRedisLimiter(nil, 10, 10, "", true)
	if allowed, err := limiter.Allow(context.Background(), "ip:1.2.3.4"); err == nil || allowed {
		t.Fatalf("Allow with nil client = %v, %v; want false, error", allowed, err)
	}
}

func TestRedisLimiterCanceledContext(t *testing.T) {
	limiter := NewRedisLimiter(nil, 10, 10, "", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Allow(ctx, "ip:1.2.3.4"); err == nil {
		t.Fatal("expected context canceled error")
	}
}

func TestRedisLimiterBurst(t *testing.T) {
	addr := os.Getenv("GOFRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("GOFRAME_TEST_REDIS_ADDR is not set, skip redis limiter tests")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}

	limiter := NewRedisLimiter(client, 1, 2, "goframe:test:ratelimit:", false)
	key := "key:" + time.Now().Format("150405.000000")
	defer client.Del(context.Background(), limiter.key(key))

	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(ctx, key)
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed=%v err=%v", i, allowed, err)
		}
	}
	if allowed, err := limiter.Allow(ctx, key); err != nil || allowed {
		t.Fatalf("third request: allowed=%v err=%v, want limited", allowed, err)
	}
}
