package cache

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/god-jason/goframe/config"
)

// setCacheEnv 设置缓存配置相关环境变量，并在测试结束后恢复原值。
func setCacheEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{
		"GOFRAME_REDIS_ENABLED",
		"GOFRAME_REDIS_ADDR",
		"GOFRAME_REDIS_KEY_PREFIX",
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

func TestBootStartupDisabledUsesMemory(t *testing.T) {
	setCacheEnv(t, map[string]string{
		"GOFRAME_REDIS_ENABLED": "false",
		"GOFRAME_REDIS_ADDR":    "",
	})
	_ = Shutdown(context.Background())

	if GetConfig().Enabled {
		t.Fatal("expected cache disabled")
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if _, ok := Get().(*MemoryCache); !ok {
		t.Fatalf("expected MemoryCache, got %T", Get())
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected cache cleared after shutdown")
	}
}

func TestBootStartupEnabledRequiresAddr(t *testing.T) {
	setCacheEnv(t, map[string]string{
		"GOFRAME_REDIS_ENABLED": "true",
		"GOFRAME_REDIS_ADDR":    "",
	})
	_ = Shutdown(context.Background())

	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected error when redis addr is empty")
	}
}

func TestBootStartupWithRedis(t *testing.T) {
	server := miniredis.RunT(t)
	setCacheEnv(t, map[string]string{
		"GOFRAME_REDIS_ENABLED":    "true",
		"GOFRAME_REDIS_ADDR":       server.Addr(),
		"GOFRAME_REDIS_KEY_PREFIX": "boot:",
	})
	_ = Shutdown(context.Background())

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	redisCache, ok := Get().(*RedisCache)
	if !ok {
		t.Fatalf("expected RedisCache, got %T", Get())
	}
	if redisCache.prefix != "boot:" {
		t.Fatalf("prefix = %q", redisCache.prefix)
	}
	if err := redisCache.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected cache cleared after shutdown")
	}
}

func TestBootStartupWithUnreachableRedis(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	addr := server.Addr()
	server.Close()

	setCacheEnv(t, map[string]string{
		"GOFRAME_REDIS_ENABLED": "true",
		"GOFRAME_REDIS_ADDR":    addr,
	})
	_ = Shutdown(context.Background())

	if err := Startup(context.Background()); err == nil {
		_ = Shutdown(context.Background())
		t.Fatal("expected startup error for unreachable redis")
	}
}
