package ratelimit

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
)

// setRateLimitEnv 设置限流与缓存配置环境变量，并在测试结束后恢复。
func setRateLimitEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_RATE",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_BURST",
		"GOFRAME_SECURITY_RATE_LIMIT_MAX_BUCKETS",
		"GOFRAME_SECURITY_RATE_LIMIT_FAIL_OPEN",
		"GOFRAME_REDIS_ENABLED",
		"GOFRAME_REDIS_ADDR",
	}
	previous := make(map[string]*string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			v := value
			previous[key] = &v
		} else {
			previous[key] = nil
		}
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("setenv %s: %v", key, err)
		}
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("config.Reload: %v", err)
	}
	t.Cleanup(func() {
		for key, value := range previous {
			if value == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *value)
			}
		}
		_ = config.Reload()
	})
}

func TestBootStartupLocalLimiter(t *testing.T) {
	setRateLimitEnv(t, map[string]string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED":      "true",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_RATE":  "10",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_BURST": "10",
		"GOFRAME_REDIS_ENABLED":                    "false",
	})
	ctx := context.Background()
	_ = Shutdown(ctx)

	if !GetConfig().Enabled {
		t.Fatal("expected limiter enabled")
	}
	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	limiter, ok := Get().(*KeyedLimiter)
	if !ok {
		t.Fatalf("expected KeyedLimiter, got %T", Get())
	}
	if allowed, err := limiter.Allow(ctx, "ip:1.1.1.1"); err != nil || !allowed {
		t.Fatalf("Allow = %v, %v", allowed, err)
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected limiter cleared after shutdown")
	}
}

func TestBootStartupDisabled(t *testing.T) {
	setRateLimitEnv(t, map[string]string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED": "false",
	})
	ctx := context.Background()
	_ = Shutdown(ctx)

	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil limiter when disabled")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestBootStartupInvalidParams(t *testing.T) {
	setRateLimitEnv(t, map[string]string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED":      "true",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_RATE":  "0",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_BURST": "10",
	})
	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected error for zero rate")
	}

	setRateLimitEnv(t, map[string]string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED":      "true",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_RATE":  "10",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_BURST": "0",
	})
	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected error for zero burst")
	}
}

func TestBootStartupRedisLimiter(t *testing.T) {
	server := miniredis.RunT(t)
	setRateLimitEnv(t, map[string]string{
		"GOFRAME_SECURITY_RATE_LIMIT_ENABLED":      "true",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_RATE":  "1",
		"GOFRAME_SECURITY_RATE_LIMIT_GLOBAL_BURST": "2",
		"GOFRAME_REDIS_ENABLED":                    "true",
		"GOFRAME_REDIS_ADDR":                       server.Addr(),
	})
	ctx := context.Background()
	_ = Shutdown(ctx)
	if err := cache.Startup(ctx); err != nil {
		t.Fatalf("cache.Startup: %v", err)
	}
	defer func() { _ = cache.Shutdown(ctx) }()

	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	limiter, ok := Get().(*RedisLimiter)
	if !ok {
		t.Fatalf("expected RedisLimiter, got %T", Get())
	}
	key := "ip:redis"
	for i := 0; i < 2; i++ {
		if allowed, err := limiter.Allow(ctx, key); err != nil || !allowed {
			t.Fatalf("request %d: allowed=%v err=%v", i, allowed, err)
		}
	}
	if allowed, err := limiter.Allow(ctx, key); err != nil || allowed {
		t.Fatalf("third request: allowed=%v err=%v", allowed, err)
	}

	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected limiter cleared after shutdown")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}
