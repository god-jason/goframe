package lock

import (
	"context"
	stderrors "errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryTryLockAndUnlock(t *testing.T) {
	locker := NewMemory()
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:1", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if _, err := locker.TryLock(ctx, "job:1", time.Minute); !stderrors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := unlock(ctx); !stderrors.Is(err, ErrNotHeld) {
		t.Fatalf("second Unlock = %v, want ErrNotHeld", err)
	}

	unlock2, err := locker.TryLock(ctx, "job:1", time.Minute)
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}
	if err := unlock2(ctx); err != nil {
		t.Fatalf("Unlock after reacquire: %v", err)
	}
}

func TestMemoryLockWaitsForRelease(t *testing.T) {
	locker := NewMemory()
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:2", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	acquired := make(chan UnlockFunc, 1)
	go func() {
		unlock, err := locker.Lock(context.Background(), "job:2", time.Minute)
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- unlock
	}()

	select {
	case <-acquired:
		t.Fatal("Lock acquired before release")
	case <-time.After(20 * time.Millisecond):
	}

	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	select {
	case next := <-acquired:
		if next == nil {
			t.Fatal("Lock returned error")
		}
		if err := next(ctx); err != nil {
			t.Fatalf("second Unlock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Lock did not acquire after release")
	}
}

func TestMemoryLockContextTimeout(t *testing.T) {
	locker := NewMemory()
	if _, err := locker.TryLock(context.Background(), "job:3", time.Minute); err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(ctx, "job:3", time.Minute); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock = %v, want context.DeadlineExceeded", err)
	}
}

func TestMemoryLockExpiry(t *testing.T) {
	locker := NewMemory()
	ctx := context.Background()

	oldUnlock, err := locker.TryLock(ctx, "job:4", 30*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	time.Sleep(60 * time.Millisecond)

	newUnlock, err := locker.TryLock(ctx, "job:4", time.Minute)
	if err != nil {
		t.Fatalf("TryLock after expiry: %v", err)
	}
	if err := oldUnlock(ctx); !stderrors.Is(err, ErrNotHeld) {
		t.Fatalf("stale Unlock = %v, want ErrNotHeld", err)
	}
	if err := newUnlock(ctx); err != nil {
		t.Fatalf("new Unlock: %v", err)
	}
}

func TestMemoryConcurrentTryLock(t *testing.T) {
	locker := NewMemory()
	ctx := context.Background()

	const workers = 64
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		holders int
		unlocks []UnlockFunc
	)

	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			unlock, err := locker.TryLock(ctx, "job:5", time.Minute)
			if err != nil {
				return
			}
			mu.Lock()
			holders++
			unlocks = append(unlocks, unlock)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if holders != 1 {
		t.Fatalf("holders = %d, want 1", holders)
	}
	for _, unlock := range unlocks {
		if err := unlock(ctx); err != nil {
			t.Fatalf("Unlock: %v", err)
		}
	}
}

func TestMemoryLockEmptyKey(t *testing.T) {
	locker := NewMemory()
	if _, err := locker.TryLock(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := locker.Lock(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
}
