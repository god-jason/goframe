package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/boot"
	_ "github.com/god-jason/goframe/bootstrap"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/storage"
	"github.com/god-jason/goframe/upload"
	"github.com/god-jason/goframe/web"
)

// TestBootStartupAndShutdown 验证按包注册的配置与 boot 任务可以完整启动和释放。
func TestBootStartupAndShutdown(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	storePath := filepath.Join(dir, "storage")
	configPath := filepath.Join(dir, "config.yaml")
	content := "" +
		"app:\n  name: integration\n  version: test\n  environment: test\n" +
		"logger:\n  level: error\n  format: json\n  output: " + logPath + "\n" +
		"storage:\n  driver: local\n  path: " + storePath + "\n" +
		"scheduler:\n  enabled: false\n" +
		"pool:\n  workers: 1\n  queue_size: 4\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config.SetFile(configPath)
	defer config.SetFile("")

	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if config.App().Name != "integration" {
		t.Fatalf("app name = %q", config.App().Name)
	}

	ctx := context.Background()
	if err := boot.Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = boot.Shutdown(shutdownCtx)
	})

	if logger.Get() == nil {
		t.Fatal("logger not initialized")
	}
	if cache.Get() == nil {
		t.Fatal("cache not initialized")
	}
	if storage.Get() == nil {
		t.Fatal("storage not initialized")
	}
	if upload.Get() == nil {
		t.Fatal("upload service not initialized")
	}
	if auth.Get() == nil {
		t.Fatal("auth manager not initialized")
	}
	if web.Engine() == nil || web.API() == nil {
		t.Fatal("web engine/router not initialized")
	}

	web.API().GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	web.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "pong" {
		t.Fatalf("ping response = %d %q", recorder.Code, recorder.Body.String())
	}

	if err := boot.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
