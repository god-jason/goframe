package lock

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newMiniRedisLocker(t *testing.T, opts RedisOptions) (*RedisLocker, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, opts), client, server
}

func TestNewRedisDefaults(t *testing.T) {
	locker := NewRedis(nil, RedisOptions{})
	defaults := DefaultRedisOptions()
	if locker.prefix != defaults.Prefix {
		t.Fatalf("prefix = %q, want %q", locker.prefix, defaults.Prefix)
	}
	if locker.retryInterval != defaults.RetryInterval {
		t.Fatalf("retryInterval = %v, want %v", locker.retryInterval, defaults.RetryInterval)
	}
	if locker.autoRenew {
		t.Fatal("autoRenew should be disabled by default")
	}

	custom := NewRedis(nil, RedisOptions{Prefix: "custom:", RetryInterval: time.Second, AutoRenew: true})
	if custom.prefix != "custom:" || custom.retryInterval != time.Second || !custom.autoRenew {
		t.Fatalf("unexpected options: %+v", custom)
	}
}

func TestRedisLockerValidation(t *testing.T) {
	ctx := context.Background()

	var nilLocker *RedisLocker
	if _, err := nilLocker.TryLock(ctx, "k", time.Minute); err == nil {
		t.Fatal("expected error for nil locker")
	}
	if _, err := nilLocker.Lock(ctx, "k", time.Minute); err == nil {
		t.Fatal("expected error for nil locker")
	}

	for _, locker := range []*RedisLocker{NewRedis(nil, RedisOptions{}), nilLocker} {
		if _, err := locker.TryLock(ctx, "", time.Minute); err == nil {
			t.Fatal("expected error for empty key")
		}
		if _, err := locker.Lock(ctx, "", time.Minute); err == nil {
			t.Fatal("expected error for empty key")
		}
		if _, err := locker.TryLock(ctx, "k", 0); err == nil {
			t.Fatal("expected error for zero ttl")
		}
		if _, err := locker.Lock(ctx, "k", 0); err == nil {
			t.Fatal("expected error for zero ttl")
		}
	}

	locker, _, _ := newMiniRedisLocker(t, RedisOptions{})
	if _, err := locker.TryLock(ctx, "", time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := locker.Lock(ctx, "", time.Minute); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := locker.TryLock(ctx, "k", 0); err == nil {
		t.Fatal("expected error for zero ttl")
	}
	if _, err := locker.Lock(ctx, "k", 0); err == nil {
		t.Fatal("expected error for zero ttl")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := locker.TryLock(canceled, "k", time.Minute); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("TryLock = %v, want context.Canceled", err)
	}
	if _, err := locker.Lock(canceled, "k", time.Minute); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Lock = %v, want context.Canceled", err)
	}
}

func TestRedisTryLockRoundTrip(t *testing.T) {
	locker, client, _ := newMiniRedisLocker(t, RedisOptions{Prefix: "test:"})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:1", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if _, err := locker.TryLock(ctx, "job:1", time.Minute); !stderrors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	n, err := client.Exists(ctx, "test:job:1").Result()
	if err != nil || n != 1 {
		t.Fatalf("redis key exists = %d, %v", n, err)
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

func TestRedisUnlockWithNilContext(t *testing.T) {
	locker, _, _ := newMiniRedisLocker(t, RedisOptions{})

	unlock, err := locker.TryLock(context.Background(), "job:nilctx", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if err := unlock(nil); err != nil {
		t.Fatalf("Unlock(nil): %v", err)
	}
}

func TestRedisStaleUnlock(t *testing.T) {
	locker, _, server := newMiniRedisLocker(t, RedisOptions{})
	ctx := context.Background()

	oldUnlock, err := locker.TryLock(ctx, "job:stale", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	server.FastForward(100 * time.Millisecond)

	newUnlock, err := locker.TryLock(ctx, "job:stale", time.Minute)
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

func TestRedisLockWaitsForRelease(t *testing.T) {
	locker, _, _ := newMiniRedisLocker(t, RedisOptions{RetryInterval: 5 * time.Millisecond})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:wait", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	acquired := make(chan UnlockFunc, 1)
	go func() {
		next, err := locker.Lock(context.Background(), "job:wait", time.Minute)
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- next
	}()

	select {
	case <-acquired:
		t.Fatal("Lock acquired before release")
	case <-time.After(30 * time.Millisecond):
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
	case <-time.After(2 * time.Second):
		t.Fatal("Lock did not acquire after release")
	}
}

func TestRedisLockContextTimeoutMiniRedis(t *testing.T) {
	locker, _, _ := newMiniRedisLocker(t, RedisOptions{RetryInterval: 5 * time.Millisecond})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:timeout", time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	defer func() { _ = unlock(ctx) }()

	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(waitCtx, "job:timeout", time.Minute); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock = %v, want context.DeadlineExceeded", err)
	}
}

func TestRedisLockSuccess(t *testing.T) {
	locker, _, _ := newMiniRedisLocker(t, RedisOptions{})
	ctx := context.Background()

	unlock, err := locker.Lock(ctx, "job:lock", time.Minute)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

func TestRedisErrorsAfterClientClose(t *testing.T) {
	locker, client, _ := newMiniRedisLocker(t, RedisOptions{})
	_ = client.Close()

	if _, err := locker.TryLock(context.Background(), "job:closed", time.Minute); err == nil {
		t.Fatal("expected TryLock error on closed client")
	}
	if _, err := locker.Lock(context.Background(), "job:closed", time.Minute); err == nil {
		t.Fatal("expected Lock error on closed client")
	}
}

func TestRedisAutoRenewKeepsLock(t *testing.T) {
	locker, _, server := newMiniRedisLocker(t, RedisOptions{AutoRenew: true})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:renew", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	// 等待续期协程至少执行一次，再推进虚拟时间。
	time.Sleep(150 * time.Millisecond)
	server.FastForward(200 * time.Millisecond)

	if _, err := locker.TryLock(ctx, "job:renew", time.Minute); !stderrors.Is(err, ErrLocked) {
		t.Fatalf("TryLock during auto renew = %v, want ErrLocked", err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

func TestRedisAutoRenewStopsWhenLockLost(t *testing.T) {
	locker, client, _ := newMiniRedisLocker(t, RedisOptions{AutoRenew: true})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:lost", 90*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if err := client.Del(ctx, "goframe:lock:job:lost").Err(); err != nil {
		t.Fatalf("Del: %v", err)
	}
	// 让续期协程发现锁已丢失并退出。
	time.Sleep(150 * time.Millisecond)

	if err := unlock(ctx); !stderrors.Is(err, ErrNotHeld) {
		t.Fatalf("Unlock = %v, want ErrNotHeld", err)
	}
}

func TestRedisAutoRenewErrorStops(t *testing.T) {
	locker, client, _ := newMiniRedisLocker(t, RedisOptions{AutoRenew: true})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:renewerr", 90*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	_ = client.Close()
	time.Sleep(80 * time.Millisecond)

	if err := unlock(ctx); err == nil {
		t.Fatal("expected release error on closed client")
	}
}

func TestRedisAutoRenewTinyTTL(t *testing.T) {
	locker, _, _ := newMiniRedisLocker(t, RedisOptions{AutoRenew: true})
	ctx := context.Background()

	unlock, err := locker.TryLock(ctx, "job:tiny", 500*time.Microsecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	// 等待续期协程执行一次，覆盖 ttl 不足 1ms 的路径。
	time.Sleep(5 * time.Millisecond)
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}
