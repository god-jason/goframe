// Package lock 提供统一互斥锁抽象，包含进程内与 Redis 两种实现。
//
// 锁用于协调同一 key 上的并发操作。TryLock 立即返回，Lock 在获取失败时
// 阻塞重试直到成功或 context 结束。所有实现都必须保证过期后不会误删
// 其他持有者重新获取的锁。
package lock

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/god-jason/goframe/id"
)

var (
	// ErrLocked 表示 key 已被其他持有者占用。
	ErrLocked = stderrors.New("lock: already locked")
	// ErrNotHeld 表示当前调用者已不再持有锁（例如已过期或被他人获取）。
	ErrNotHeld = stderrors.New("lock: not held")
)

// UnlockFunc 释放锁。重复调用应返回 ErrNotHeld，而不是删除他人的锁。
type UnlockFunc func(context.Context) error

// Locker 是框架统一锁接口。
// ttl 表示锁的存活时间，实现可用它防止持有者崩溃导致死锁；
// 具体实现可对 ttl<=0 或过期时间做出额外约定。
type Locker interface {
	// TryLock 尝试获取锁，失败时立即返回 ErrLocked。
	TryLock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error)
	// Lock 获取锁，必要时阻塞重试；ctx 取消或超时时返回 ctx 错误。
	Lock(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error)
}

// newToken 生成锁持有者令牌，用于解锁时校验归属。
func newToken() string { return id.New() }
