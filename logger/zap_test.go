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
