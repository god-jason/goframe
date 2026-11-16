package storage

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/god-jason/goframe/config"
)

func newTestStorage(t *testing.T) *LocalStorage {
	t.Helper()
	store, err := NewLocal(config.StorageConfig{Driver: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return store
}

func TestLocalStorageRoundTrip(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	if err := store.Put(ctx, "tenant/2026/device.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ok, err := store.Exists(ctx, "tenant/2026/device.txt")
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}

	reader, err := store.Get(ctx, "tenant/2026/device.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", data)
	}

	if err := store.Delete(ctx, "tenant/2026/device.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, _ = store.Exists(ctx, "tenant/2026/device.txt")
	if ok {
		t.Fatal("expected file to be deleted")
	}
}

func TestLocalStorageRejectsTraversal(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()
	if _, err := store.Get(ctx, "../secret.txt"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if err := store.Put(ctx, "a/../../b.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestLocalStorageGetMissing(t *testing.T) {
	store := newTestStorage(t)
	if _, err := store.Get(context.Background(), "missing.txt"); err == nil {
		t.Fatal("expected missing file error")
	}
}
