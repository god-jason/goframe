package taskqueue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"

	"github.com/god-jason/goframe/config"
)

func TestDefaultConfigAndNormalize(t *testing.T) {
	defaults := DefaultConfig()
	if defaults.Concurrency != 10 || defaults.MaxRetry != 3 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	if defaults.Queues["default"] == 0 || defaults.Queues["notify"] == 0 {
		t.Fatalf("default queues missing: %+v", defaults.Queues)
	}

	bad := Config{Concurrency: -1, ShutdownTimeout: -1, MaxRetry: -1, TaskTimeout: -1, LogLevel: "  "}
	got := bad.normalize()
	if got.Concurrency != defaults.Concurrency || got.ShutdownTimeout != defaults.ShutdownTimeout {
		t.Fatalf("normalize = %+v", got)
	}
	if got.MaxRetry != defaults.MaxRetry || got.TaskTimeout != defaults.TaskTimeout {
		t.Fatalf("normalize = %+v", got)
	}
	if got.LogLevel != defaults.LogLevel || len(got.Queues) == 0 {
		t.Fatalf("normalize = %+v", got)
	}
}

func TestValidateRequiresAddr(t *testing.T) {
	if err := (Config{Enabled: false}).Validate(); err != nil {
		t.Fatalf("disabled config must validate: %v", err)
	}
	if err := (Config{Enabled: true, Addr: "127.0.0.1:6379"}).Validate(); err != nil {
		t.Fatalf("explicit addr: %v", err)
	}
	if err := (Config{Enabled: true}).Validate(); err == nil {
		t.Fatal("expected error when no redis addr configured")
	}
}

func TestRedisOptionsFallbackToCacheSection(t *testing.T) {
	t.Setenv("GOFRAME_CONFIG", "")
	config.SetFile("")
	t.Setenv("GOFRAME_REDIS_ADDR", "127.0.0.1:6399")
	t.Setenv("GOFRAME_REDIS_PASSWORD", "secret")
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	opt := Config{}.redisOptions()
	if opt.Addr != "127.0.0.1:6399" || opt.Password != "secret" {
		t.Fatalf("fallback options = %+v", opt)
	}

	explicit := Config{Addr: "10.0.0.1:6380", Username: "u", Password: "p", DB: 3}.redisOptions()
	if explicit.Addr != "10.0.0.1:6380" || explicit.Username != "u" || explicit.DB != 3 {
		t.Fatalf("explicit options = %+v", explicit)
	}
}

func TestRegisterPanics(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
	}{
		{"empty type", func() { Register("  ", func(context.Context, []byte) error { return nil }) }},
		{"nil handler", func() { Register("test.panic.nil", nil) }},
		{"duplicate", func() {
			Register("test.panic.dup", func(context.Context, []byte) error { return nil })
			Register("test.panic.dup", func(context.Context, []byte) error { return nil })
		}},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s: expected panic", tc.name)
				}
			}()
			tc.fn()
		}()
	}
}

func TestEnqueueWithoutServer(t *testing.T) {
	if err := Enqueue(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for empty task type")
	}
	if err := Enqueue(context.Background(), "test.any", []byte("x")); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
	if Enabled() {
		t.Fatal("Enabled should be false before startup")
	}
}

func startQueue(t *testing.T, concurrency int) {
	t.Helper()
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "taskqueue:\n" +
		"  enabled: true\n" +
		"  addr: " + mr.Addr() + "\n" +
		"  concurrency: " + strconv.Itoa(concurrency) + "\n" +
		"  max_retry: 1\n" +
		"  task_timeout: 10s\n" +
		"  queues:\n" +
		"    default: 3\n" +
		"    notify: 5\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	t.Cleanup(func() { config.SetFile("") })
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background()) })
}

func TestEnqueueProcessRoundtrip(t *testing.T) {
	done := make(chan string, 4)
	Register("test.echo", func(_ context.Context, payload []byte) error {
		done <- string(payload)
		return nil
	})
	startQueue(t, 2)

	if !Enabled() {
		t.Fatal("Enabled should be true after startup")
	}

	// 启动后注册必须 panic。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic when registering after start")
			}
		}()
		Register("test.late", func(context.Context, []byte) error { return nil })
	}()

	if err := Enqueue(context.Background(), "test.echo", []byte("hello"), Queue("notify")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case got := <-done:
		if got != "hello" {
			t.Fatalf("payload = %q", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for task processing")
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := Enqueue(context.Background(), "test.echo", []byte("x")); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err after shutdown = %v", err)
	}
}

func TestStartupDisabled(t *testing.T) {
	config.SetFile("")
	t.Setenv("GOFRAME_CONFIG", "")
	if err := config.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("disabled startup: %v", err)
	}
	if Enabled() {
		t.Fatal("disabled queue must not report Enabled")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown when disabled: %v", err)
	}
}

func TestAsynqLogLevelMapping(t *testing.T) {
	cases := map[string]asynq.LogLevel{
		"debug":   asynq.DebugLevel,
		"WARN":    asynq.WarnLevel,
		"error":   asynq.ErrorLevel,
		"fatal":   asynq.FatalLevel,
		"unknown": asynq.InfoLevel,
		"":        asynq.InfoLevel,
	}
	for input, want := range cases {
		if got := asynqLogLevel(input); got != want {
			t.Fatalf("asynqLogLevel(%q) = %v, want %v", input, got, want)
		}
	}
}
