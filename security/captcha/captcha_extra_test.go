package captcha

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubStore struct {
	code   string
	getErr error
	setErr error
	delErr error

	getCalls int
	setCalls int
	delCalls int
}

func (s *stubStore) Set(context.Context, string, string, time.Duration) error {
	s.setCalls++
	return s.setErr
}

func (s *stubStore) Get(context.Context, string) (string, error) {
	s.getCalls++
	return s.code, s.getErr
}

func (s *stubStore) Delete(context.Context, string) error {
	s.delCalls++
	return s.delErr
}

func TestGenerateStoreFailure(t *testing.T) {
	store := &stubStore{setErr: errors.New("store down")}
	manager, err := New(Config{Length: 4}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := manager.Generate(context.Background()); err == nil {
		t.Fatal("expected store error")
	}
	if store.setCalls != 1 {
		t.Fatalf("set calls = %d, want 1", store.setCalls)
	}
}

func TestGenerateCanceledContext(t *testing.T) {
	manager, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Generate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestVerifyStoreErrors(t *testing.T) {
	manager, err := New(Config{}, &stubStore{code: "1234", getErr: errors.New("load failed")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Verify(context.Background(), "id", "1234"); err == nil {
		t.Fatal("expected load error")
	}

	deleteStore := &stubStore{code: "1234", delErr: errors.New("delete failed")}
	manager, err = New(Config{}, deleteStore)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.Verify(context.Background(), "id", "1234"); err == nil {
		t.Fatal("expected delete error")
	}
	if deleteStore.delCalls == 0 {
		t.Fatal("expected delete to be attempted")
	}
}

func TestVerifyCanceledContext(t *testing.T) {
	manager, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Verify(ctx, "id", "1234"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestMemoryStoreContextErrors(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Set(ctx, "id", "1234", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set err = %v", err)
	}
	if _, err := store.Get(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get err = %v", err)
	}
	if err := store.Delete(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete err = %v", err)
	}
}

func TestMemoryStoreNoExpiry(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "forever", "1234", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	code, err := store.Get(ctx, "forever")
	if err != nil || code != "1234" {
		t.Fatalf("Get = %q, %v", code, err)
	}
}

func TestMemoryStoreSweepRemovesExpiredEntries(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if err := store.Set(ctx, "old", "1111", time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	store.mu.Lock()
	store.lastSweep = time.Now().Add(-2 * memorySweepInterval)
	store.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	// 触发清理：Set 会调用 sweepLocked。
	if err := store.Set(ctx, "new", "2222", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := store.Get(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after sweep", err)
	}
	if code, err := store.Get(ctx, "new"); err != nil || code != "2222" {
		t.Fatalf("Get new = %q, %v", code, err)
	}
}

func TestVerifyCaseInsensitiveCode(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(Config{Length: 4}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	challenge, err := manager.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	code, err := store.Get(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manager.Verify(context.Background(), "  "+challenge.ID+"  ", code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
