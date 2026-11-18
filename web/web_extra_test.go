package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/health"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/security/ratelimit"
	"github.com/god-jason/goframe/websocket"
)

// applyConfig 写入临时 YAML 配置并重载，测试结束后恢复默认配置。
func applyConfig(t *testing.T, content string) {
	t.Helper()
	if strings.TrimSpace(content) == "" {
		config.SetFile("")
		if err := config.Reload(); err != nil {
			t.Fatalf("reload defaults: %v", err)
		}
	} else {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		config.SetFile(path)
		if err := config.Reload(); err != nil {
			t.Fatalf("reload config: %v", err)
		}
	}
	t.Cleanup(func() {
		config.SetFile("")
		if err := config.Reload(); err != nil {
			t.Errorf("restore config: %v", err)
		}
		webOptions = WebOptions{}
	})
}

func yamlPath(path string) string {
	return strings.ReplaceAll(path, `\`, "/")
}

func TestConfigureOptions(t *testing.T) {
	t.Cleanup(func() { webOptions = WebOptions{} })

	Configure(nil, WithFileSystem(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("x")}}))
	if webOptions.FileSystem == nil {
		t.Fatal("filesystem not applied")
	}
	Configure(func(o *WebOptions) { o.FileSystem = nil })
	if webOptions.FileSystem != nil {
		t.Fatal("filesystem not reset")
	}
}

func TestStartupDefaultAndGetters(t *testing.T) {
	applyConfig(t, "server:\n  port: 8080\n  mode: release\n")
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(context.Background()) }()

	engine := Engine()
	if engine == nil || Router() == nil || API() == nil || Health() == nil || Metrics() == nil {
		t.Fatal("startup did not initialize globals")
	}
	if Addr() == "" {
		t.Fatal("addr must not be empty before listen")
	}

	for _, target := range []string{"/health", "/health/live", "/metrics"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", target, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("no-route status = %d", recorder.Code)
	}
}

func TestStartupPProfRateLimitCacheAndWebSocket(t *testing.T) {
	applyConfig(t, `
server:
  port: 8080
  request_timeout: 1s
  trusted_proxies:
    - 127.0.0.1
pprof:
  enabled: true
  path: /debug/pprof
websocket:
  enabled: true
`)

	if err := cache.Startup(context.Background()); err != nil {
		t.Fatalf("cache startup: %v", err)
	}
	t.Cleanup(func() { _ = cache.Shutdown(context.Background()) })
	if err := ratelimit.Startup(context.Background()); err != nil {
		t.Fatalf("ratelimit startup: %v", err)
	}
	t.Cleanup(func() { _ = ratelimit.Shutdown(context.Background()) })
	if err := websocket.Startup(context.Background()); err != nil {
		t.Fatalf("websocket startup: %v", err)
	}
	t.Cleanup(func() { _ = websocket.Shutdown(context.Background()) })

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(context.Background()) }()

	if Health() == nil || Router() == nil {
		t.Fatal("health checker not initialized")
	}

	paths := make(map[string]bool)
	for _, route := range Engine().Routes() {
		paths[route.Path] = true
	}
	if !paths["/ws"] {
		t.Fatalf("websocket route missing: %+v", paths)
	}
	if !paths["/debug/pprof/"] {
		t.Fatalf("pprof route missing: %+v", paths)
	}
}

func TestStartupCompressionDisabledAndSecurityDisabled(t *testing.T) {
	applyConfig(t, "server:\n  port: 8080\n  compression: false\nsecurity:\n  enabled: false\n")
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestStartupSecurityHeadersDisabled(t *testing.T) {
	applyConfig(t, `
server:
  port: 8080
security:
  enabled: true
  headers:
    enabled: false
  cors:
    enabled: true
    allowed_origins:
      - https://example.com
`)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestStartupCORSError(t *testing.T) {
	applyConfig(t, `
server:
  port: 8080
security:
  cors:
    enabled: true
    allowed_origins:
      - "*"
    allow_credentials: true
`)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected cors configuration error")
	}
}

func TestStartupValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"invalid server", "server:\n  port: 0\n", "port"},
		{"invalid tls", "server:\n  port: 8080\ntls:\n  mode: bogus\n", "tls"},
		{"invalid proxy", "server:\n  port: 8080\n  trusted_proxies:\n    - not-an-ip\n", "trusted proxy"},
		{"invalid certificate", "server:\n  port: 8080\ntls:\n  mode: file\n  cert_file: missing.crt\n  key_file: missing.key\n", "cert"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applyConfig(t, tc.yaml)
			err := Startup(context.Background())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestSetupNoRouteModes(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o600); err != nil {
			t.Fatalf("write index: %v", err)
		}
		applyConfig(t, "server:\n  port: 8080\nspa:\n  enabled: true\n  mode: directory\n  path: \""+yamlPath(dir)+"\"\n")
		if err := Startup(context.Background()); err != nil {
			t.Fatalf("Startup: %v", err)
		}
		defer func() { _ = Shutdown(context.Background()) }()

		recorder := httptest.NewRecorder()
		Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
		if !strings.Contains(recorder.Body.String(), "spa") {
			t.Fatalf("body = %q", recorder.Body.String())
		}
	})

	t.Run("directory missing index", func(t *testing.T) {
		applyConfig(t, "server:\n  port: 8080\nspa:\n  enabled: true\n  mode: directory\n  path: \""+yamlPath(t.TempDir())+"\"\n")
		if err := Startup(context.Background()); err == nil {
			t.Fatal("expected missing index error")
		}
	})

	t.Run("embed without filesystem", func(t *testing.T) {
		applyConfig(t, "server:\n  port: 8080\nspa:\n  enabled: true\n  mode: embed\n")
		webOptions = WebOptions{}
		if err := Startup(context.Background()); err == nil {
			t.Fatal("expected embed filesystem error")
		}
	})

	t.Run("embed with filesystem", func(t *testing.T) {
		applyConfig(t, "server:\n  port: 8080\nspa:\n  enabled: true\n  mode: embed\n")
		Configure(WithFileSystem(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>embed</html>")}}))
		if err := Startup(context.Background()); err != nil {
			t.Fatalf("Startup: %v", err)
		}
		defer func() { _ = Shutdown(context.Background()) }()

		recorder := httptest.NewRecorder()
		Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/any", nil))
		if !strings.Contains(recorder.Body.String(), "embed") {
			t.Fatalf("body = %q", recorder.Body.String())
		}
	})

	t.Run("unsupported mode", func(t *testing.T) {
		applyConfig(t, "server:\n  port: 8080\nspa:\n  enabled: true\n  mode: magic\n")
		if err := Startup(context.Background()); err == nil {
			t.Fatal("expected unsupported spa mode error")
		}
	})
}

func TestAPIBasePath(t *testing.T) {
	cases := []struct {
		cfg  Config
		want string
	}{
		{Config{APIPrefix: "/api/", APIVersion: "v1"}, "/api/v1"},
		{Config{APIPrefix: "/api", APIVersion: "/v2/"}, "/api/v2"},
		{Config{APIPrefix: "/api/", APIVersion: ""}, "/api"},
	}
	for _, tc := range cases {
		if got := apiBasePath(tc.cfg); got != tc.want {
			t.Fatalf("apiBasePath(%+v) = %q, want %q", tc.cfg, got, tc.want)
		}
	}
}

func installTestGlobals(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv, err := New(Options{Config: testServerConfig(), Handler: gin.New(), Logger: logger.NewNop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checker := health.New("test")

	oldServer, oldHealth := global.server, global.health
	global.server, global.health = srv, checker

	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
		global.server, global.health = oldServer, oldHealth
	})
	return srv
}

func waitReady(t *testing.T, checker *health.Checker) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !checker.IsReady() {
		if time.Now().After(deadline) {
			t.Fatal("server did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunContextCancel(t *testing.T) {
	installTestGlobals(t)
	checker := global.health

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx) }()

	waitReady(t, checker)
	if Addr() == "" || strings.HasSuffix(Addr(), ":0") {
		t.Fatalf("addr = %q", Addr())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestRunServeError(t *testing.T) {
	srv := installTestGlobals(t)
	checker := global.health

	done := make(chan error, 1)
	go func() { done <- Run(context.Background()) }()
	waitReady(t, checker)

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after server shutdown")
	}
}

func TestRunNilContext(t *testing.T) {
	srv := installTestGlobals(t)
	checker := global.health

	done := make(chan error, 1)
	go func() { done <- Run(nil) }()
	waitReady(t, checker)

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run(nil): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run(nil) did not return")
	}
}

func TestWebStartAndRunErrors(t *testing.T) {
	cfg := testServerConfig()
	cfg.Host = "invalid host"
	srv, err := New(Options{Config: cfg, Handler: gin.New(), Logger: logger.NewNop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	oldServer := global.server
	global.server = srv
	t.Cleanup(func() {
		global.server = oldServer
	})

	if err := Start(); err == nil {
		t.Fatal("expected start error")
	}
	if err := Run(context.Background()); err == nil {
		t.Fatal("expected run start error")
	}
}

func TestWebWithoutServer(t *testing.T) {
	oldServer, oldRouter := global.server, global.router
	global.server, global.router = nil, nil
	t.Cleanup(func() {
		global.server, global.router = oldServer, oldRouter
	})

	if Addr() != "" {
		t.Fatalf("addr = %q", Addr())
	}
	if API() != nil {
		t.Fatal("API must be nil without router")
	}
	if err := Start(); err == nil {
		t.Fatal("expected start error without server")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown without server: %v", err)
	}
}
