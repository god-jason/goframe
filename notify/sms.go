package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// smsRegistry 保存各云厂商包在 boot 启动时注册的短信服务商，支持多厂商故障切换。
var smsRegistry = struct {
	mu   sync.RWMutex
	list []Provider
}{}

// AddSMSProvider 注册一个全局短信服务商，供 channel=sms 的通知使用。
// 由 cloud/aliyun、cloud/tencent、cloud/huawei 等厂商包在启动时调用。
func AddSMSProvider(p Provider) {
	if p == nil {
		return
	}
	smsRegistry.mu.Lock()
	smsRegistry.list = append(smsRegistry.list, p)
	smsRegistry.mu.Unlock()
}

// SMSProviders 返回当前已注册短信服务商的快照。
func SMSProviders() []Provider {
	smsRegistry.mu.RLock()
	defer smsRegistry.mu.RUnlock()
	list := make([]Provider, len(smsRegistry.list))
	copy(list, smsRegistry.list)
	return list
}

// ClearSMSProviders 清空短信服务商注册表，主要用于测试。
func ClearSMSProviders() {
	smsRegistry.mu.Lock()
	smsRegistry.list = nil
	smsRegistry.mu.Unlock()
}

// registryProvider 在发送时动态读取注册表，使厂商注册与 boot 启动顺序解耦。
type registryProvider struct{}

func (registryProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	list := SMSProviders()
	if len(list) == 0 {
		return fmt.Errorf("notify: no sms provider registered (enable a cloud sms section)")
	}
	return NewMulti(list...).Send(ctx, to, content, params)
}

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
