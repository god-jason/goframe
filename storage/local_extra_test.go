package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestNewLocalRejectsFileAsRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := NewLocal(Config{Path: file}); err == nil {
		t.Fatal("expected error when path is a file")
	}
}

func TestLocalStoragePutReaderError(t *testing.T) {
	store := newTestStorage(t)
	err := store.Put(context.Background(), "a.txt", errReader{err: errors.New("boom")})
	if err == nil || !strings.Contains(err.Error(), "write file") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalStoragePutMkdirError(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()
	// 让 "a.txt" 成为普通文件，随后把它当作目录使用时创建目录会失败。
	if err := store.Put(ctx, "a.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Put(ctx, "a.txt/b.txt", strings.NewReader("x")); err == nil {
		t.Fatal("expected mkdir error")
	}
}

func TestLocalStorageDeleteDirectoryFails(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()
	if err := store.Put(ctx, "dir/nested.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, "dir"); err == nil {
		t.Fatal("expected delete non-empty directory to fail")
	}
	// 目录存在但 Exists 应返回 false。
	ok, err := store.Exists(ctx, "dir")
	if err != nil || ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
}

func TestLocalStorageExistsErrors(t *testing.T) {
	store := newTestStorage(t)
	if _, err := store.Exists(context.Background(), "../evil"); err == nil {
		t.Fatal("expected invalid key error")
	}
}

func TestLocalStorageGetURLRelative(t *testing.T) {
	store := newTestStorage(t)
	url, err := store.GetURL(context.Background(), "/tenant/a.png", 0)
	if err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	if url != "/tenant/a.png" {
		t.Fatalf("url = %q", url)
	}
}

func TestLocalStorageInvalidKeys(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()
	for _, key := range []string{"\x00bad", "a\\b", "   "} {
		if err := store.Put(ctx, key, strings.NewReader("x")); err == nil {
			t.Fatalf("Put(%q) expected error", key)
		}
		if _, err := store.Get(ctx, key); err == nil {
			t.Fatalf("Get(%q) expected error", key)
		}
		if err := store.Delete(ctx, key); err == nil {
			t.Fatalf("Delete(%q) expected error", key)
		}
		if _, err := store.Exists(ctx, key); err == nil {
			t.Fatalf("Exists(%q) expected error", key)
		}
		if _, err := store.GetURL(ctx, key, time.Minute); err == nil {
			t.Fatalf("GetURL(%q) expected error", key)
		}
	}
}

func TestLocalStoragePutEmptyKey(t *testing.T) {
	store := newTestStorage(t)
	_ = store.Put(context.Background(), "", strings.NewReader("x"))
}

var _ io.Reader = errReader{}
