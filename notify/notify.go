// Package notify 提供邮件、Webhook、短信等统一消息通知能力。
//
// 各发送器相互独立，可按需组合使用；具体发送器由业务侧创建后注入，
// 包本身不绑定任何第三方厂商或全局配置。
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
