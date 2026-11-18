package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
)

func withAuthConfig(t *testing.T, cfg Config) {
	t.Helper()
	previous := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = previous
		_ = Shutdown(context.Background())
		SetSessionRepository(nil)
	})
}

func TestConfigValidate(t *testing.T) {
	base := DefaultConfig()
	base.Secret = strings.Repeat("k", 32)

	if err := base.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	disabled := base
	disabled.Enabled = false
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled config rejected: %v", err)
	}

	mutations := []func(*Config){
		func(c *Config) { c.AccessTokenTTL = 0 },
		func(c *Config) { c.RefreshTokenTTL = c.AccessTokenTTL },
		func(c *Config) { c.Password.Algorithm = "bcrypt" },
		func(c *Config) { c.Password.Memory = 1024 },
		func(c *Config) { c.Password.Iterations = 0 },
		func(c *Config) { c.Password.Parallelism = 0 },
	}
	for i, mutate := range mutations {
		cfg := base
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("mutation %d should be rejected", i)
		}
	}

	app := config.App()
	previousEnv := *app
	app.Environment = "production"
	t.Cleanup(func() { *app = previousEnv })

	production := base
	production.Secret = "short"
	if err := production.Validate(); err == nil {
		t.Fatal("expected short production secret to be rejected")
	}
	production.Secret = strings.Repeat("s", 32)
	if err := production.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestBootLifecycle(t *testing.T) {
	withAuthConfig(t, testAuthConfig())
	SetSessionRepository(nil)

	if GetConfig() == nil {
		t.Fatal("GetConfig must not be nil")
	}
	// 未启动时 Get 返回 nil。
	_ = Shutdown(context.Background())
	if Get() != nil {
		t.Fatal("Get should be nil before startup")
	}

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("Get should return manager after startup")
	}
	manager := Get()
	if _, _, err := manager.Login(context.Background(), &Identity{UserID: "u1"}, SessionMeta{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("Get should be nil after shutdown")
	}
}

func TestBootUsesCustomRepository(t *testing.T) {
	withAuthConfig(t, testAuthConfig())
	repo := NewMemorySessionRepository()
	SetSessionRepository(repo)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background()) })

	_, identity, err := Get().Login(context.Background(), &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := repo.GetSession(context.Background(), identity.SessionID); err != nil {
		t.Fatalf("custom repository missing session: %v", err)
	}
}

func TestBootDisabledAndInvalidConfig(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Enabled = false
	withAuthConfig(t, cfg)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("disabled Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled auth must not create a manager")
	}

	cfg = testAuthConfig()
	cfg.AccessTokenTTL = -time.Second
	withAuthConfig(t, cfg)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected invalid config to fail startup")
	}
}
