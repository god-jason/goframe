package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/logger"
)

func testServerConfig() Config {
	cfg := DefaultConfig()
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
	material, err := buildTLSConfig(TLSConfig{Mode: "none"}, logger.NewNop())
	if err != nil || material.config != nil {
		t.Fatalf("none mode = %+v, %v", material, err)
	}

	if _, err := buildTLSConfig(TLSConfig{Mode: "file", CertFile: "missing.crt", KeyFile: "missing.key"}, logger.NewNop()); err == nil {
		t.Fatal("expected missing certificate error")
	}

	acmeCfg := TLSConfig{
		Mode: "acme",
		ACME: ACMEConfig{
			Email:             "admin@example.com",
			Domains:           []string{"example.com"},
			CacheDir:          t.TempDir(),
			HTTPChallengeAddr: "127.0.0.1:0",
		},
	}
	acmeMaterial, err := buildTLSConfig(acmeCfg, logger.NewNop())
	if err != nil {
		t.Fatalf("acme config: %v", err)
	}
	if acmeMaterial.config == nil || acmeMaterial.challenge == nil {
		t.Fatalf("unexpected acme material: %+v", acmeMaterial)
	}

	acmeCfg.ACME.Challenge = "tls-alpn-01"
	alpnMaterial, err := buildTLSConfig(acmeCfg, logger.NewNop())
	if err != nil {
		t.Fatalf("acme tls-alpn: %v", err)
	}
	if alpnMaterial.challenge != nil {
		t.Fatal("tls-alpn mode must not start http challenge server")
	}

	acmeCfg.ACME.Challenge = "dns-01"
	if _, err := buildTLSConfig(acmeCfg, logger.NewNop()); err == nil {
		t.Fatal("expected dns-01 unsupported error")
	}

	if _, err := buildTLSConfig(TLSConfig{Mode: "unknown"}, logger.NewNop()); err == nil {
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
