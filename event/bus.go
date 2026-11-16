package event

import (
	"context"
	stderrors "errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/god-jason/goframe/logger"
)

// Bus 是进程内事件总线实现。
// 同一事件的所有订阅者按注册顺序同步执行，任一订阅者失败不会影响其他订阅者，
// 所有错误会聚合后返回。
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler
	log      logger.Logger
}

// New 创建进程内事件总线。
func New(log logger.Logger) *Bus {
	if log == nil {
		log = logger.NewNop()
	}
	return &Bus{handlers: make(map[string][]EventHandler), log: log}
}

// Publish 发布事件。
// 订阅者按注册顺序同步执行；单个订阅者失败或 panic 不会影响其他订阅者。
func (b *Bus) Publish(ctx context.Context, evt Event) error {
	if evt.Name == "" {
		return fmt.Errorf("event: name must not be empty")
	}
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now().UTC()
	}

	b.mu.RLock()
	handlers := append([]EventHandler(nil), b.handlers[evt.Name]...)
	b.mu.RUnlock()

	started := time.Now()
	var errs []error
	for _, handler := range handlers {
		if err := safeCall(ctx, handler, evt); err != nil {
			b.log.Error("event handler failed",
				logger.String("event", evt.Name),
				logger.Error(err),
			)
			errs = append(errs, err)
		}
	}
	b.log.Debug("event published",
		logger.String("event", evt.Name),
		logger.Int("subscribers", len(handlers)),
		logger.Int("failed", len(errs)),
		logger.Duration("elapsed", time.Since(started)),
	)
	return stderrors.Join(errs...)
}

// Subscribe 订阅事件。
func (b *Bus) Subscribe(name string, handler EventHandler) error {
	if name == "" {
		return fmt.Errorf("event: name must not be empty")
	}
	if handler == nil {
		return fmt.Errorf("event: handler is nil")
	}
	b.mu.Lock()
	b.handlers[name] = append(b.handlers[name], handler)
	count := len(b.handlers[name])
	b.mu.Unlock()
	b.log.Debug("event subscribed",
		logger.String("event", name),
		logger.Int("subscribers", count),
	)
	return nil
}

// Unsubscribe 取消某个事件的全部订阅。
func (b *Bus) Unsubscribe(name string) error {
	b.mu.Lock()
	delete(b.handlers, name)
	b.mu.Unlock()
	b.log.Debug("event unsubscribed", logger.String("event", name))
	return nil
}

// safeCall 执行订阅者并隔离 panic。
// 订阅者属于业务代码，panic 会转换为错误返回，同时保留堆栈到日志。
func safeCall(ctx context.Context, handler EventHandler, evt Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("event: handler panic: %v\n%s", r, debug.Stack())
		}
	}()
	return handler(ctx, evt)
}
