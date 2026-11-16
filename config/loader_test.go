package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const loaderTestConfig = `app:
  name: loader-test
  version: ${GOF_TEST_VERSION:-9.9.9}
server:
  port: 18080
  read_timeout: 5s
database:
  driver: mysql
  dsn: ${GOF_TEST_DSN:-}
security:
  request:
    max_body_size: 2MB
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadFromFileWithEnvExpansion(t *testing.T) {
	t.Setenv("GOF_TEST_VERSION", "")
	t.Setenv("GOF_TEST_DSN", "user:pass@tcp(localhost:3306)/demo")

	cfg, err := Load(WithFile(writeConfig(t, loaderTestConfig)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.App.Name != "loader-test" || cfg.App.Version != "9.9.9" {
		t.Fatalf("unexpected app config: %+v", cfg.App)
	}
	if cfg.Server.Port != 18080 || cfg.Server.ReadTimeout != 5*time.Second {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.Database.DSN != "user:pass@tcp(localhost:3306)/demo" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
	if cfg.Security.Request.MaxBodySize != 2*1024*1024 {
		t.Fatalf("max body size = %d", cfg.Security.Request.MaxBodySize)
	}
	if cfg.Server.MaxHeaderBytes != Default().Server.MaxHeaderBytes {
		t.Fatal("expected unspecified values to keep defaults")
	}
	if cfg.APIBasePath() != "/api/v1" {
		t.Fatalf("api base path = %q", cfg.APIBasePath())
	}
	if cfg.Server.Addr() != "0.0.0.0:18080" {
		t.Fatalf("addr = %q", cfg.Server.Addr())
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	t.Setenv("GOF_TEST_DSN", "user:pass@tcp(localhost:3306)/demo")
	t.Setenv("GOFRAME_SERVER_PORT", "18081")
	t.Setenv("DB_DSN", "override:pass@tcp(localhost:3306)/demo")

	cfg, err := Load(WithFile(writeConfig(t, loaderTestConfig)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 18081 {
		t.Fatalf("port = %d, want 18081", cfg.Server.Port)
	}
	if cfg.Database.DSN != "override:pass@tcp(localhost:3306)/demo" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
}

func TestLoadMissingExplicitFile(t *testing.T) {
	_, err := Load(WithFile(filepath.Join(t.TempDir(), "missing.yaml")))
	if err == nil {
		t.Fatal("expected missing config file error")
	}
}

func TestLoadValidationFailure(t *testing.T) {
	content := `app:
  name: invalid
  environment: production
`
	_, err := Load(WithFile(writeConfig(t, content)))
	if err == nil {
		t.Fatal("expected production validation error")
	}
	if !strings.Contains(err.Error(), "security.auth.secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadWithoutConfigFileUsesDefaults(t *testing.T) {
	cfg, err := Load(WithSearchPaths(t.TempDir()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Name != Default().App.Name {
		t.Fatalf("app name = %q", cfg.App.Name)
	}
	if cfg.Server.Port != Default().Server.Port {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
}

func TestWatchRequiresConfigFile(t *testing.T) {
	loader := NewLoader(WithSearchPaths(t.TempDir()))
	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loader.Watch(context.Background(), func(*Config) {}); err == nil {
		t.Fatal("expected watch without config file to fail")
	}
}

func TestWatchReloadsConfig(t *testing.T) {
	path := writeConfig(t, loaderTestConfig)
	t.Setenv("GOF_TEST_DSN", "user:pass@tcp(localhost:3306)/demo")

	loader := NewLoader(WithFile(path))
	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := make(chan *Config, 4)
	if err := loader.Watch(ctx, func(cfg *Config) { changes <- cfg }); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	updated := strings.Replace(loaderTestConfig, "port: 18080", "port: 18082", 1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
			t.Fatalf("rewrite config: %v", err)
		}
		select {
		case cfg := <-changes:
			if cfg.Server.Port != 18082 {
				t.Fatalf("port = %d, want 18082", cfg.Server.Port)
			}
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Fatal("config watcher did not reload the file")
}
