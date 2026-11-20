package payment

import (
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	got := Config{}.normalize()
	defaults := DefaultConfig()
	if got.NotifyPath != defaults.NotifyPath {
		t.Fatalf("NotifyPath = %q, want %q", got.NotifyPath, defaults.NotifyPath)
	}
	if got.Timeout != defaults.Timeout {
		t.Fatalf("Timeout = %v, want %v", got.Timeout, defaults.Timeout)
	}
	if got.Idempotency.DoneTTL != defaults.Idempotency.DoneTTL {
		t.Fatalf("DoneTTL = %v, want %v", got.Idempotency.DoneTTL, defaults.Idempotency.DoneTTL)
	}
	if got.Idempotency.ProcessingTTL != defaults.Idempotency.ProcessingTTL {
		t.Fatalf("ProcessingTTL = %v, want %v", got.Idempotency.ProcessingTTL, defaults.Idempotency.ProcessingTTL)
	}

	// 处理锁不应长于完成标记，否则回退为 1 秒。
	got = Config{Idempotency: IdempotencyConfig{DoneTTL: time.Minute, ProcessingTTL: time.Hour}}.normalize()
	if got.Idempotency.ProcessingTTL != time.Second {
		t.Fatalf("ProcessingTTL = %v, want 1s fallback", got.Idempotency.ProcessingTTL)
	}
}

func TestValidate(t *testing.T) {
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("disabled config must pass: %v", err)
	}

	err := Config{Enabled: true}.Validate()
	if err == nil {
		t.Fatal("enabled config without channels must fail")
	}

	wechat := Config{Enabled: true, Wechat: WechatConfig{
		Enabled: true, AppID: "wx", MchID: "1900", SerialNo: "SN", PrivateKey: "pem", APIv3Key: "key",
	}}
	if err := wechat.Validate(); err != nil {
		t.Fatalf("complete wechat config must pass: %v", err)
	}
	for _, field := range []struct {
		name  string
		mutate func(*WechatConfig)
	}{
		{"app_id", func(c *WechatConfig) { c.AppID = "" }},
		{"mch_id", func(c *WechatConfig) { c.MchID = "" }},
		{"serial_no", func(c *WechatConfig) { c.SerialNo = "" }},
		{"private_key", func(c *WechatConfig) { c.PrivateKey = "" }},
		{"apiv3_key", func(c *WechatConfig) { c.APIv3Key = "" }},
	} {
		missing := wechat
		field.mutate(&missing.Wechat)
		if err := missing.Validate(); err == nil {
			t.Fatalf("wechat config without %s must fail", field.name)
		}
	}

	alipayNormal := Config{Enabled: true, Alipay: AlipayConfig{
		Enabled: true, AppID: "2021", PrivateKey: "pem", PublicKey: "pub",
	}}
	if err := alipayNormal.Validate(); err != nil {
		t.Fatalf("complete alipay config must pass: %v", err)
	}
	for _, field := range []struct {
		name   string
		mutate func(*AlipayConfig)
	}{
		{"app_id", func(c *AlipayConfig) { c.AppID = "" }},
		{"private_key", func(c *AlipayConfig) { c.PrivateKey = "" }},
		{"public_key", func(c *AlipayConfig) { c.PublicKey = "" }},
	} {
		missing := alipayNormal
		field.mutate(&missing.Alipay)
		if err := missing.Validate(); err == nil {
			t.Fatalf("alipay normal mode without %s must fail", field.name)
		}
	}

	alipayCert := Config{Enabled: true, Alipay: AlipayConfig{
		Enabled: true, AppID: "2021", PrivateKey: "pem",
		AppCertFile: "app.crt", AlipayCertFile: "ali.crt", RootCertFile: "root.crt",
	}}
	if err := alipayCert.Validate(); err != nil {
		t.Fatalf("alipay cert mode must pass: %v", err)
	}
	for _, field := range []struct {
		name   string
		mutate func(*AlipayConfig)
	}{
		{"app_cert_file", func(c *AlipayConfig) { c.AppCertFile = "" }},
		{"alipay_cert_file", func(c *AlipayConfig) { c.AlipayCertFile = "" }},
		{"root_cert_file", func(c *AlipayConfig) { c.RootCertFile = "" }},
	} {
		missingCert := alipayCert
		field.mutate(&missingCert.Alipay)
		if err := missingCert.Validate(); err == nil {
			t.Fatalf("alipay cert mode without %s must fail", field.name)
		}
	}
	if !alipayCert.Alipay.certMode() || alipayNormal.Alipay.certMode() {
		t.Fatal("certMode detection incorrect")
	}
}
