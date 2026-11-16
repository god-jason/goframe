package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return string(data)
}

func TestZapLoggerLevelFilteringAndSetLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	log, err := New(Options{Level: "warn", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = log.Close() }()

	log.Debug("debug message")
	log.Info("info message")
	log.Warn("warn message")
	log.Error("error message")
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	content := readFile(t, path)
	if strings.Contains(content, "debug message") || strings.Contains(content, "info message") {
		t.Fatalf("expected lower levels to be filtered: %s", content)
	}
	if !strings.Contains(content, "warn message") || !strings.Contains(content, "error message") {
		t.Fatalf("expected warn/error to be logged: %s", content)
	}

	if err := log.SetLevel("debug"); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	log.Debug("debug after level change")
	_ = log.Sync()
	if !strings.Contains(readFile(t, path), "debug after level change") {
		t.Fatal("expected debug log after level change")
	}
}

func TestZapLoggerWithFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	log, err := New(Options{Level: "info", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = log.Close() }()

	child := log.With(String("module", "auth"), String("user_id", "u1"))
	child.Info("login succeeded")
	_ = log.Sync()

	content := readFile(t, path)
	for _, expected := range []string{`"module":"auth"`, `"user_id":"u1"`, `"message":"login succeeded"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("log does not contain %q: %s", expected, content)
		}
	}
}

func TestNewRejectsInvalidLevel(t *testing.T) {
	if _, err := New(Options{Level: "verbose"}); err == nil {
		t.Fatal("expected invalid level error")
	}
}

func TestChildLoggerCloseDoesNotCloseRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	root, err := New(Options{Level: "info", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = root.Close() }()

	child, ok := root.With(String("module", "test")).(*ZapLogger)
	if !ok {
		t.Fatal("With should return *ZapLogger")
	}
	if err := child.Close(); err != nil {
		t.Fatalf("child Close: %v", err)
	}

	root.Info("after child close")
	if err := root.Sync(); err != nil {
		t.Fatalf("root Sync: %v", err)
	}
	if !strings.Contains(readFile(t, path), "after child close") {
		t.Fatal("root logger should still be usable after child Close")
	}
}

func TestZapLoggerRotatesLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "app.log")
	log, err := New(Options{
		Level:  "info",
		Format: "json",
		Output: path,
		Rotate: RotateOptions{MaxSize: 1 << 20, MaxBackups: 3},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = log.Close() }()

	// 每次写入约 900KB，累计超过 1MB 后触发切分。
	payload := strings.Repeat("x", 900*1024)
	log.Info(payload)
	log.Info(payload)
	log.Info(payload)
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log file missing: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "logs", "app-*.log"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("expected at least one rotated log file")
	}
}

func TestZapLoggerRotationCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	log, err := New(Options{
		Level:  "info",
		Format: "json",
		Output: path,
		Rotate: RotateOptions{Disabled: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 单文件超过滚动阈值也不应产生备份。
	log.Info(strings.Repeat("y", 2<<20))
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	backups, err := filepath.Glob(filepath.Join(dir, "app-*.log*"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 0 {
		t.Fatalf("unexpected rotated files: %v", backups)
	}
}

func TestRotateDefaults(t *testing.T) {
	if got := rotateMaxSizeMB(0); got != 100 {
		t.Fatalf("rotateMaxSizeMB(0) = %d, want 100", got)
	}
	if got := rotateMaxSizeMB(512 << 10); got != 1 {
		t.Fatalf("rotateMaxSizeMB(512KB) = %d, want 1", got)
	}
	if got := rotateMaxSizeMB(50 << 20); got != 50 {
		t.Fatalf("rotateMaxSizeMB(50MB) = %d, want 50", got)
	}
	if got := positiveOr(0, 30); got != 30 {
		t.Fatalf("positiveOr(0, 30) = %d, want 30", got)
	}
	if got := positiveOr(7, 30); got != 7 {
		t.Fatalf("positiveOr(7, 30) = %d, want 7", got)
	}
}

func TestNopLogger(t *testing.T) {
	log := NewNop()
	log.Debug("d", String("k", "v"))
	log.Info("i")
	log.Warn("w")
	log.Error("e")
	if log.With(String("k", "v")) == nil {
		t.Fatal("With must return a logger")
	}
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}
