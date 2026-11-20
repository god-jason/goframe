package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	failingShutdownOnce sync.Once
	failingShutdown     atomic.Bool
)

// ensureFailingShutdownTask 只注册一次失败任务，通过开关控制是否返回错误，
// 避免 -count>1 时全局 boot 注册表重复注册或污染其他用例。
func ensureFailingShutdownTask(t *testing.T) {
	t.Helper()
	failingShutdownOnce.Do(func() {
		err := boot.Register("example-failing-shutdown", &boot.Module{
			Shutdown: func(context.Context) error {
				if failingShutdown.Load() {
					return fmt.Errorf("shutdown boom")
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("register failing task: %v", err)
		}
	})
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestRunContextFlagHandling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout, stderr bytes.Buffer
	if code := runContext(ctx, []string{"-h"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help = %d", code)
	}
	stderr.Reset()
	if code := runContext(ctx, []string{"-unknown-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown flag = %d", code)
	}

	// run 在帮助参数下立即返回，不会启动任何组件。
	if code := run([]string{"-h"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("run help = %d", code)
	}
}

func TestRunContextStartupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Cleanup(func() { config.SetFile("") })
	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("startup failure = %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("run:")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunContextLifecycle(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`app:
  name: example-test
  version: test
  environment: test
logger:
  level: error
  format: json
  output: %s
storage:
  driver: local
  path: %s
scheduler:
  enabled: false
pool:
  workers: 1
  queue_size: 4
server:
  host: 127.0.0.1
  port: %d
`, filepath.Join(dir, "app.log"), filepath.Join(dir, "storage"), port)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Cleanup(func() { config.SetFile("") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", configPath}, io.Discard, &stderr)
	if code != 0 {
		t.Fatalf("run = %d, %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "app.log")); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
}

func TestRunContextModuleInitFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`app:
  name: example-test
  environment: test
logger:
  level: error
  format: json
  output: %s
security:
  auth:
    enabled: false
    password:
      memory: 1024
      iterations: 1
      parallelism: 1
storage:
  driver: local
  path: %s
scheduler:
  enabled: false
pool:
  workers: 1
  queue_size: 4
server:
  host: 127.0.0.1
  port: %d
`, filepath.Join(dir, "app.log"), filepath.Join(dir, "storage"), freePort(t))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Cleanup(func() { config.SetFile("") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", configPath}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("module init failure = %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("initialize module:")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunContextWebRunError(t *testing.T) {
	dir := t.TempDir()
	// 占用端口以让 web.Run 返回错误。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`app:
  name: example-test
  environment: test
logger:
  level: error
  format: json
  output: %s
storage:
  driver: local
  path: %s
scheduler:
  enabled: false
pool:
  workers: 1
  queue_size: 4
server:
  host: 127.0.0.1
  port: %d
`, filepath.Join(dir, "app.log"), filepath.Join(dir, "storage"), port)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Cleanup(func() { config.SetFile("") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", configPath}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("web error code = %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("run web:")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunContextShutdownError(t *testing.T) {
	ensureFailingShutdownTask(t)
	failingShutdown.Store(true)
	t.Cleanup(func() { failingShutdown.Store(false) })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`app:
  name: example-test
  environment: test
logger:
  level: error
  format: json
  output: %s
storage:
  driver: local
  path: %s
scheduler:
  enabled: false
pool:
  workers: 1
  queue_size: 4
server:
  host: 127.0.0.1
  port: %d
`, filepath.Join(dir, "app.log"), filepath.Join(dir, "storage"), freePort(t))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Cleanup(func() { config.SetFile("") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", configPath}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("shutdown error code = %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("shutdown:")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
