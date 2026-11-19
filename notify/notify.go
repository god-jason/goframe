// Package notify 提供邮件、Webhook、短信等统一消息通知能力。
//
// 各发送器相互独立，可按需组合使用，也可由业务侧直接创建。
// 全局用法：notify 配置段声明通道（smtp / webhook），
// notify.Send 在任务队列（taskqueue）启用时入队异步发送并自动重试，
// 队列未启用时回退为同步直发。
package notify

import "context"

// Message 是统一消息结构。
type Message struct {
	To       []string          `json:"to"`
	Subject  string            `json:"subject"`
	Body     string            `json:"body"`
	HTML     bool              `json:"html"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Sender 是消息发送接口。
type Sender interface {
	Send(ctx context.Context, msg Message) error
}
