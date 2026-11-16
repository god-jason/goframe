package database

import (
	"context"
	"database/sql"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/logger"
)

func newDatabaseLogger(t *testing.T) (logger.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database.log")
	log, err := logger.New(logger.Options{Level: "debug", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log, path
}

func readDatabaseLog(t *testing.T, log logger.Logger, path string) string {
	t.Helper()
	if err := log.Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func TestGormLoggerWritesLevelsAndSlowQueries(t *testing.T) {
	log, path := newDatabaseLogger(t)
	gormLog := newGormLogger(log, "info", 5*time.Millisecond).(*gormLogger)
	ctx := context.Background()

	gormLog.Info(ctx, "info message %d", 1)
	gormLog.Warn(ctx, "warn message")
	gormLog.Error(ctx, "error message")

	gormLog.Trace(ctx, time.Now().Add(-time.Second), func() (string, int64) { return "SELECT 1", 1 }, nil)
	gormLog.Trace(ctx, time.Now(), func() (string, int64) { return "SELECT bad", 0 }, stderrors.New("boom"))

	content := readDatabaseLog(t, log, path)
	for _, expected := range []string{
		"info message 1",
		"warn message",
		"error message",
		"database slow query",
		"database query failed",
		"SELECT 1",
		"boom",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("log does not contain %q: %s", expected, content)
		}
	}
}

func TestGormLoggerSilentMode(t *testing.T) {
	log, path := newDatabaseLogger(t)
	gormLog := newGormLogger(log, "info", time.Millisecond)
	silent := gormLog.LogMode(gormlogger.Silent)

	ctx := context.Background()
	silent.Info(ctx, "silent info")
	silent.Warn(ctx, "silent warn")
	silent.Error(ctx, "silent error")
	silent.Trace(ctx, time.Now().Add(-time.Second), func() (string, int64) { return "SELECT", 0 }, stderrors.New("silent trace"))

	content := readDatabaseLog(t, log, path)
	if strings.Contains(content, "silent") {
		t.Fatalf("silent logger must not write anything: %s", content)
	}
}

func TestOpenRejectsUnsupportedDriver(t *testing.T) {
	cfg := Config{Driver: "unknown", DSN: "dsn"}
	if _, err := Open(cfg, logger.NewNop()); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestNilDatabaseSafety(t *testing.T) {
	db := &gormDatabase{}
	if err := db.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error on uninitialized database")
	}
	if db.Stats() != (sql.DBStats{}) {
		t.Fatalf("unexpected stats: %+v", db.Stats())
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
