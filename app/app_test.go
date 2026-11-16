package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/response"
)

type routeModule struct{}

func (m *routeModule) Name() string { return "route-test" }

func (m *routeModule) Init(*App) error { return nil }

func (m *routeModule) RegisterRoutes(router gin.IRouter) {
	router.GET("/ping", func(c *gin.Context) {
		response.Success(c, gin.H{"pong": true})
	})
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func testAppConfig(t *testing.T, port int) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.App.Name = "app-test"
	cfg.App.Version = "test"
	cfg.App.Environment = "test"
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = port
	cfg.Server.Mode = "release"
	cfg.Server.ShutdownTimeout = 3 * time.Second
	cfg.Storage.Path = t.TempDir()
	cfg.Scheduler.Enabled = false
	cfg.Logger.Level = "error"
	return cfg
}

func waitFor(t *testing.T, description string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", description)
}

func TestRunServesHealthAndModuleRoutes(t *testing.T) {
	port := freePort(t)
	cfg := testAppConfig(t, port)

	application, err := New(WithConfig(&cfg), WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := application.Register(&routeModule{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- application.Run(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	waitFor(t, "health ready", func() bool {
		resp, err := client.Get(baseURL + "/health/ready")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	resp, err := client.Get(baseURL + "/api/v1/ping")
	if err != nil {
		t.Fatalf("GET ping: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("ping status = %d", resp.StatusCode)
	}
	var body response.Response[map[string]any]
	decodeErr := json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode ping: %v", decodeErr)
	}
	if body.Code != 0 || body.Data["pong"] != true {
		t.Fatalf("unexpected ping response: %+v", body)
	}

	// 后台任务应通过应用协程池执行，且 Shutdown 会等待其退出。
	taskDone := make(chan struct{})
	if err := application.Submit(context.Background(), func(context.Context) error {
		close(taskDone)
		return nil
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-taskDone:
	case <-time.After(3 * time.Second):
		t.Fatal("background task did not run")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("application did not stop in time")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Port = 0
	if _, err := New(WithConfig(&cfg), WithLogger(logger.NewNop())); err == nil {
		t.Fatal("expected invalid port to be rejected")
	}
}

func TestRegisterNilModule(t *testing.T) {
	cfg := testAppConfig(t, 18092)
	application, err := New(WithConfig(&cfg), WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := application.Register(nil); err == nil {
		t.Fatal("expected nil module error")
	}
}

func TestInitIsIdempotentAndRegisterAfterInitFails(t *testing.T) {
	cfg := testAppConfig(t, 18090)
	application, err := New(WithConfig(&cfg), WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := application.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := application.Init(context.Background()); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if err := application.Register(&routeModule{}); err == nil {
		t.Fatal("expected register after init to fail")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := application.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := application.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestSPAEmbedRequiresFileSystem(t *testing.T) {
	cfg := testAppConfig(t, 18091)
	cfg.SPA.Enabled = true
	cfg.SPA.Mode = "embed"
	cfg.SPA.Path = "web/dist"

	application, err := New(WithConfig(&cfg), WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := application.Init(context.Background()); err == nil {
		t.Fatal("expected spa embed without filesystem to fail")
	}
}
