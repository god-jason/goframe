package lock

import (
	"context"
	stderrors "errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newRedisClient 返回测试用 Redis 客户端；未设置 GOFRAME_TEST_REDIS_ADDR 时跳过测试。
func newRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("GOFRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("GOFRAME_TEST_REDIS_ADDR is not set, skip redis lock tests")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	return client
}

func TestRedisTryLockAndUnlock(t *testing.T) {
	client := newRedisClient(t)
	locker := NewRedis(client, RedisOptions{Prefix: "goframe:test:lock:"})
	ctx := context.Background()
	key := "job:" + time.Now().Format("150405.000000")

	unlock, err := locker.TryLock(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if _, err := locker.TryLock(ctx, key, time.Minute); !stderrors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := unlock(ctx); !stderrors.Is(err, ErrNotHeld) {
		t.Fatalf("second Unlock = %v, want ErrNotHeld", err)
	}

	unlock2, err := locker.TryLock(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}
	if err := unlock2(ctx); err != nil {
		t.Fatalf("Unlock after reacquire: %v", err)
	}
}

func TestRedisLockExpiry(t *testing.T) {
	client := newRedisClient(t)
	locker := NewRedis(client, RedisOptions{Prefix: "goframe:test:lock:"})
	ctx := context.Background()
	key := "expiry:" + time.Now().Format("150405.000000")

	oldUnlock, err := locker.TryLock(ctx, key, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	newUnlock, err := locker.TryLock(ctx, key, time.Minute)
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

func TestRedisLockAutoRenew(t *testing.T) {
	client := newRedisClient(t)
	locker := NewRedis(client, RedisOptions{Prefix: "goframe:test:lock:", AutoRenew: true})
	ctx := context.Background()
	key := "renew:" + time.Now().Format("150405.000000")

	unlock, err := locker.TryLock(ctx, key, 120*time.Millisecond)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	// 超过一个 ttl，若未自动续期锁应已过期。
	time.Sleep(300 * time.Millisecond)
	if _, err := locker.TryLock(ctx, key, time.Minute); !stderrors.Is(err, ErrLocked) {
		t.Fatalf("TryLock during auto renew = %v, want ErrLocked", err)
	}
	if err := unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	next, err := locker.TryLock(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}
	if err := next(ctx); err != nil {
		t.Fatalf("Unlock after reacquire: %v", err)
	}
}

func TestRedisLockContextTimeout(t *testing.T) {
	client := newRedisClient(t)
	locker := NewRedis(client, RedisOptions{
		Prefix:        "goframe:test:lock:",
		RetryInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()
	key := "timeout:" + time.Now().Format("150405.000000")

	unlock, err := locker.TryLock(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	defer func() { _ = unlock(ctx) }()

	waitCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(waitCtx, key, time.Minute); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock = %v, want context.DeadlineExceeded", err)
	}
}
