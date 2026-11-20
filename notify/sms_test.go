package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeProvider struct {
	err  error
	sent [][]string
}

func (p *fakeProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, to)
	return nil
}

func TestSMSRegistry(t *testing.T) {
	ClearSMSProviders()
	t.Cleanup(ClearSMSProviders)

	if got := SMSProviders(); len(got) != 0 {
		t.Fatalf("providers = %d", len(got))
	}

	// 空注册表时发送应报错。
	if err := (registryProvider{}).Send(context.Background(), []string{"13800000000"}, "x", nil); err == nil {
		t.Fatal("expected error with no providers")
	}

	AddSMSProvider(nil) // 忽略 nil
	first := &fakeProvider{err: errors.New("boom")}
	second := &fakeProvider{}
	AddSMSProvider(first)
	AddSMSProvider(second)

	if got := SMSProviders(); len(got) != 2 {
		t.Fatalf("providers = %d", len(got))
	}

	// 首个失败应自动切换第二个。
	if err := (registryProvider{}).Send(context.Background(), []string{"13800000000"}, "x", nil); err != nil {
		t.Fatalf("failover: %v", err)
	}
	if len(second.sent) != 1 {
		t.Fatalf("second.sent = %v", second.sent)
	}
}

func TestBuildSenderSMSChannel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Channel = "sms"
	sender, err := cfg.buildSender()
	if err != nil {
		t.Fatalf("buildSender: %v", err)
	}
	if _, ok := sender.(*SMS); !ok {
		t.Fatalf("sender type = %T", sender)
	}

	cfg.Channel = "carrier-pigeon"
	if _, err := cfg.buildSender(); err == nil {
		t.Fatal("expected error for unknown channel")
	} else if !strings.Contains(err.Error(), "sms") {
		t.Fatalf("err = %v", err)
	}
}
