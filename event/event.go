// Package event 定义进程内事件总线。
package event

import (
	"context"
	"time"
)

// Event 是业务事件。进程内事件总线不保证可靠投递，关键流程应使用
// 事务 Outbox 或可靠消息队列。
type Event struct {
	Name       string            `json:"name"`
	Payload    any               `json:"payload"`
	OccurredAt time.Time         `json:"occurred_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// EventHandler 是事件处理器。
type EventHandler func(ctx context.Context, event Event) error

// EventBus 是框架统一事件总线接口。
type EventBus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(name string, handler EventHandler) error
	Unsubscribe(name string) error
}
