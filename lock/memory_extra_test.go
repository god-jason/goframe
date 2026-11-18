package lock

import (
	"context"
	stderrors "errors"
	"testing"
	"time"
)

func TestMemoryTryLockCanceledContext(t *testing.T) {
	locker := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := locker.TryLock(ctx, "job:cancel", time.Minute); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("TryLock = %v, want context.Canceled", err)
	}
}

func TestMemoryLockCanceledContext(t *testing.T) {
	locker := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := locker.Lock(ctx, "job:cancel", time.Minute); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Lock = %v, want context.Canceled", err)
	}
}

func TestMemoryUnlockAfterEntryRemoved(t *testing.T) {
	locker := NewMemory()
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:removed", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	locker.mu.Lock()
	delete(locker.entries, "job:removed")
	locker.mu.Unlock()

	if err := unlock(ctx); !stderrors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock = %v, want ErrNotHeld", err)
	}
}

func TestMemoryLockBackoffUntilDeadline(t *testing.T) {
	locker := NewMemory()
	if _, err := locker.TryLock(context.Background(), "job:backoff", time.Minute); err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(ctx, "job:backoff", time.Minute); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock = %v, want context.DeadlineExceeded", err)
	}
}
