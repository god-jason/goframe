package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLocalStorageGetURL(t *testing.T) {
	store, err := NewLocal(Config{
		Driver:  "local",
		Path:    t.TempDir(),
		BaseURL: "https://cdn.example.com/files/",
	})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	url, err := store.GetURL(context.Background(), "tenant/a.png", time.Hour)
	if err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	if url != "https://cdn.example.com/files/tenant/a.png" {
		t.Fatalf("url = %q", url)
	}

	for _, key := range []string{"", "../escape", "a\\b", "a/../b"} {
		if _, err := store.GetURL(context.Background(), key, time.Hour); err == nil {
			t.Fatalf("expected key %q to be rejected", key)
		}
	}
}

func TestLocalStorageCanceledContext(t *testing.T) {
	store, err := NewLocal(Config{Driver: "local", Path: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Put(ctx, "a.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected canceled context error on Put")
	}
	if _, err := store.Get(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error on Get")
	}
	if err := store.Delete(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error on Delete")
	}
	if _, err := store.Exists(ctx, "a.txt"); err == nil {
		t.Fatal("expected canceled context error on Exists")
	}
	if _, err := store.GetURL(ctx, "a.txt", time.Hour); err == nil {
		t.Fatal("expected canceled context error on GetURL")
	}
}

func TestNewRejectsUnsupportedDriver(t *testing.T) {
	if _, err := New(Config{Driver: "unknown"}); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}
