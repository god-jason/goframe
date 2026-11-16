package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

func testServerConfig() config.ServerConfig {
	cfg := config.Default().Server
	cfg.Host = "127.0.0.1"
	cfg.Port = 0
	cfg.ShutdownTimeout = 2 * time.Second
	return cfg
}

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	srv, err := New(Options{
		Config:  testServerConfig(),
		Handler: engine,
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, engine
}

func TestServerStartServeAndShutdown(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- srv.Wait() }()

	response, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("unexpected response: %d %q", response.StatusCode, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop in time")
	}

	if got := srv.ShutdownTimeout(); got != 2*time.Second {
		t.Fatalf("shutdown timeout = %v", got)
	}
}

func TestNewRejectsNilHandler(t *testing.T) {
	if _, err := New(Options{Config: testServerConfig(), Logger: logger.NewNop()}); err == nil {
		t.Fatal("expected nil handler error")
	}
}

func TestBuildTLSConfig(t *testing.T) {
	tlsConfig, err := buildTLSConfig(config.TLSConfig{Mode: "none"})
	if err != nil || tlsConfig != nil {
		t.Fatalf("none mode = %v, %v", tlsConfig, err)
	}

	if _, err := buildTLSConfig(config.TLSConfig{Mode: "file", CertFile: "missing.crt", KeyFile: "missing.key"}); err == nil {
		t.Fatal("expected missing certificate error")
	}
	if _, err := buildTLSConfig(config.TLSConfig{Mode: "acme", ACME: config.ACMEConfig{Domains: []string{"example.com"}}}); err == nil {
		t.Fatal("expected acme mode to be rejected")
	}
	if _, err := buildTLSConfig(config.TLSConfig{Mode: "unknown"}); err == nil {
		t.Fatal("expected unsupported tls mode error")
	}
}

func TestServerAlreadyStarted(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if !strings.Contains(srv.Addr(), "127.0.0.1:") {
		t.Fatalf("addr = %q", srv.Addr())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
