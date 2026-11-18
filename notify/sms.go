package notify

import (
	"context"
	"errors"
	"fmt"
)

// Provider 是短信服务商适配接口，由具体厂商实现，包本身不绑定厂商。
type Provider interface {
	Send(ctx context.Context, to []string, content string, params map[string]string) error
}

// Multi 按顺序尝试多个短信服务商，任一成功即返回。
type Multi struct {
	providers []Provider
}

var _ Provider = (*Multi)(nil)

// NewMulti 创建短信服务商组合。
func NewMulti(providers ...Provider) *Multi {
	return &Multi{providers: providers}
}

// Send 依次调用服务商，全部失败时返回合并错误。
func (m *Multi) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var errs []error
	for _, provider := range m.providers {
		if provider == nil {
			continue
		}
		if err := provider.Send(ctx, to, content, params); err == nil {
			return nil
		} else {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return fmt.Errorf("notify: no sms provider configured")
	}
	return fmt.Errorf("notify: all sms providers failed: %w", errors.Join(errs...))
}

// SMS 把统一 Message 适配为特定短信服务商。
type SMS struct {
	provider Provider
}

var _ Sender = (*SMS)(nil)

// NewSMS 创建短信发送器。
func NewSMS(provider Provider) *SMS {
	return &SMS{provider: provider}
}

// Send 使用 To 作为接收号码、Body 作为内容、Metadata 作为服务商参数。
func (s *SMS) Send(ctx context.Context, msg Message) error {
	if s.provider == nil {
		return fmt.Errorf("notify: sms provider is nil")
	}
	if len(msg.To) == 0 {
		return fmt.Errorf("notify: message has no recipients")
	}
	return s.provider.Send(ctx, msg.To, msg.Body, msg.Metadata)
}
