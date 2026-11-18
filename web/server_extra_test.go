package web

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/logger"
)

func newHandlerEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return engine
}

func TestServerTLSFileMode(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeSelfSignedCertificate(t, dir, time.Hour)

	srv, err := New(Options{
		Config:  testServerConfig(),
		TLS:     TLSConfig{Mode: "file", CertFile: certFile, KeyFile: keyFile},
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var shutdownCalled int32
	srv.RegisterOnShutdown(func() { atomic.StoreInt32(&shutdownCalled, 1) })

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 自签测试证书
	}}
	response, err := client.Get("https://" + srv.Addr() + "/")
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
	if atomic.LoadInt32(&shutdownCalled) != 1 {
		t.Fatal("shutdown callback not invoked")
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestNewWithoutLogger(t *testing.T) {
	srv, err := New(Options{Config: testServerConfig(), Handler: newHandlerEngine()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before start: %v", err)
	}
}

func TestNewRejectsInvalidTLSMaterial(t *testing.T) {
	_, err := New(Options{
		Config:  testServerConfig(),
		TLS:     TLSConfig{Mode: "file", CertFile: "missing.crt", KeyFile: "missing.key"},
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err == nil {
		t.Fatal("expected certificate load error")
	}
}

func TestServerStartListenError(t *testing.T) {
	cfg := testServerConfig()
	cfg.Host = "invalid host"
	srv, err := New(Options{Config: cfg, Handler: newHandlerEngine(), Logger: logger.NewNop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err == nil {
		t.Fatal("expected listen error")
	}
	if srv.Addr() != cfg.Addr() {
		t.Fatalf("addr = %q, want %q", srv.Addr(), cfg.Addr())
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before start: %v", err)
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait before start: %v", err)
	}
}

func acmeTestConfig(t *testing.T, challenge, addr string) TLSConfig {
	return TLSConfig{
		Mode: "acme",
		ACME: ACMEConfig{
			Email:             "admin@example.com",
			Domains:           []string{"example.com"},
			Challenge:         challenge,
			CacheDir:          t.TempDir(),
			HTTPChallengeAddr: addr,
		},
	}
}

func TestServerACMECHallengeLifecycle(t *testing.T) {
	srv, err := New(Options{
		Config:  testServerConfig(),
		TLS:     acmeTestConfig(t, "http-01", "127.0.0.1:0"),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestServerACMEChallengeRequiredFailure(t *testing.T) {
	srv, err := New(Options{
		Config:  testServerConfig(),
		TLS:     acmeTestConfig(t, "http-01", "127.0.0.1:99999"),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err == nil || !strings.Contains(err.Error(), "acme challenge") {
		t.Fatalf("err = %v", err)
	}
}

func TestServerACMEChallengeOptionalFailure(t *testing.T) {
	srv, err := New(Options{
		Config:  testServerConfig(),
		TLS:     acmeTestConfig(t, "", "127.0.0.1:99999"),
		Handler: newHandlerEngine(),
		Logger:  logger.NewNop(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestServerShutdownWithCanceledContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/slow", func(c *gin.Context) {
		close(started)
		<-release
		c.String(http.StatusOK, "done")
	})

	srv, err := New(Options{Config: testServerConfig(), Handler: engine, Logger: logger.NewNop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get("http://" + srv.Addr() + "/slow")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	<-started

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Shutdown(canceled); err == nil {
		t.Fatal("expected shutdown error for canceled context")
	}

	close(release)
	<-requestDone

	ctx, cancelTimeout := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelTimeout()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
