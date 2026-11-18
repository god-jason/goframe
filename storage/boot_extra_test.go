package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/god-jason/goframe/config"
)

func TestNewDispatchesLocalDriver(t *testing.T) {
	store, err := New(Config{Driver: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := store.(*LocalStorage); !ok {
		t.Fatalf("store = %T, want *LocalStorage", store)
	}
}

func TestStartupShutdown(t *testing.T) {
	path := t.TempDir()
	t.Setenv("GOFRAME_STORAGE_DRIVER", "local")
	t.Setenv("GOFRAME_STORAGE_PATH", path)
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if current := GetConfig(); current == nil || current.Driver != "local" {
		t.Fatalf("GetConfig = %+v", current)
	}
	if Get() != nil {
		t.Fatal("Get should be nil before Startup")
	}

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("Get should return storage after Startup")
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("Get should be nil after Shutdown")
	}
}

func TestStartupPropagatesError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	t.Setenv("GOFRAME_STORAGE_DRIVER", "local")
	t.Setenv("GOFRAME_STORAGE_PATH", file)
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected Startup error")
	}
	_ = Shutdown(context.Background())
}
