package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withConfig(t *testing.T, cfg Config) {
	t.Helper()
	previous := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = previous
		_ = Shutdown(context.Background())
	})
}

func TestBootStartupReloadAndShutdown(t *testing.T) {
	dir := t.TempDir()
	withConfig(t, Config{
		Level:  "info",
		Format: "json",
		Output: filepath.Join(dir, "first.log"),
		Rotation: RotationConfig{
			Enabled:    true,
			MaxSize:    1 << 20,
			MaxAge:     1,
			MaxBackups: 1,
		},
	})

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	instance, ok := Get().(*ZapLogger)
	if !ok {
		t.Fatalf("Get() = %T, want *ZapLogger", Get())
	}

	// 已启动时再次 Startup 会关闭旧实例。
	*GetConfig() = Config{
		Level:  "debug",
		Format: "console",
		Output: filepath.Join(dir, "second.log"),
		Rotation: RotationConfig{
			Enabled:    true,
			MaxSize:    1 << 20,
			MaxAge:     1,
			MaxBackups: 1,
		},
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("second Startup: %v", err)
	}
	if Get() == instance {
		t.Fatal("expected logger instance to be replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "second.log")); err != nil {
		t.Fatalf("second log file missing: %v", err)
	}

	// applyReload 仅在支持 SetLevel 的实现上调整等级。
	*GetConfig() = Config{Level: "warn"}
	applyReload()
	Get().Warn("reload level applied")
	if err := Get().Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, ok := Get().(nopLogger); !ok {
		t.Fatalf("Get() after Shutdown = %T, want nopLogger", Get())
	}
	// 重复关闭应为无操作。
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestApplyReloadWithoutLevelSetter(t *testing.T) {
	previous := global.logger
	global.logger = NewNop()
	t.Cleanup(func() { global.logger = previous })

	applyReload()
}

func TestStartupValidationErrors(t *testing.T) {
	cases := []Config{
		{Level: "verbose", Format: "json", Output: ""},
		{Level: "info", Format: "yaml", Output: ""},
		{Level: "info", Format: "json", Rotation: RotationConfig{Enabled: true}},
		{Level: "info", Format: "json", Rotation: RotationConfig{Enabled: true, MaxSize: 1}},
		{Level: "info", Format: "json", Rotation: RotationConfig{Enabled: true, MaxSize: 1, MaxAge: 1}},
	}
	for _, cfg := range cases {
		previous := *GetConfig()
		*GetConfig() = cfg
		err := Startup(context.Background())
		*GetConfig() = previous
		if err == nil {
			t.Fatalf("Startup(%+v) should fail", cfg)
		}
	}
}

func TestStartupPropagatesNewError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	withConfig(t, Config{
		Level:  "info",
		Format: "json",
		Output: filepath.Join(blocker, "logs", "app.log"),
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected Startup to fail for invalid output path")
	}
}

func TestOptionsFromConfig(t *testing.T) {
	cfg := Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
		Rotation: RotationConfig{
			Enabled:    false,
			MaxSize:    2 << 20,
			MaxAge:     7,
			MaxBackups: 3,
			Compress:   true,
			LocalTime:  true,
		},
	}
	opts := optionsFromConfig(cfg)
	if opts.Level != "debug" || opts.Format != "console" || opts.Output != "stdout" {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if !opts.Rotate.Disabled || opts.Rotate.MaxSize != 2<<20 || opts.Rotate.MaxAge != 7 ||
		opts.Rotate.MaxBackups != 3 || !opts.Rotate.Compress || !opts.Rotate.LocalTime {
		t.Fatalf("unexpected rotate options: %+v", opts.Rotate)
	}
}

func TestNewConsoleStdoutAndStderr(t *testing.T) {
	stdout, err := New(Options{Level: "info", Format: "console", Output: "STDOUT"})
	if err != nil {
		t.Fatalf("New stdout: %v", err)
	}
	stdout.Info("console stdout message")
	if err := stdout.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stderr, err := New(Options{Level: "info", Format: "console", Output: "stderr"})
	if err != nil {
		t.Fatalf("New stderr: %v", err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewOutputErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	if _, err := New(Options{Level: "info", Format: "json", Output: filepath.Join(blocker, "app.log")}); err == nil {
		t.Fatal("expected mkdir error")
	}

	// 目录作为输出文件时 OpenFile 失败，分别覆盖滚动与禁用滚动分支。
	if _, err := New(Options{Level: "info", Format: "json", Output: dir}); err == nil {
		t.Fatal("expected open error with rotation enabled")
	}
	if _, err := New(Options{Level: "info", Format: "json", Output: dir, Rotate: RotateOptions{Disabled: true}}); err == nil {
		t.Fatal("expected open error with rotation disabled")
	}
}

func TestParseLevelVariants(t *testing.T) {
	cases := map[string]string{
		"":        "info",
		"INFO":    "info",
		"debug":   "debug",
		"warn":    "warn",
		"warning": "warn",
		"error":   "error",
	}
	for input, want := range cases {
		level, err := parseLevel(input)
		if err != nil {
			t.Fatalf("parseLevel(%q): %v", input, err)
		}
		if level.String() != want {
			t.Fatalf("parseLevel(%q) = %s, want %s", input, level.String(), want)
		}
	}
	if _, err := parseLevel("verbose"); err == nil {
		t.Fatal("expected unknown level error")
	}
}

func TestNewUsesDefaultOutputPath(t *testing.T) {
	t.Chdir(t.TempDir())
	log, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info("default output")
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join("logs", "app.log")); err != nil {
		t.Fatalf("default log file missing: %v", err)
	}
}

func TestSetLevelRejectsInvalidValue(t *testing.T) {
	log, err := New(Options{Level: "info", Format: "json", Output: filepath.Join(t.TempDir(), "app.log")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = log.Close() }()
	if err := log.SetLevel("verbose"); err == nil {
		t.Fatal("expected invalid level error")
	}
	if err := log.SetLevel(strings.ToUpper("debug")); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
}
