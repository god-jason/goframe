package aliyun

import (
	"context"
	"strings"
	"testing"
	"time"
)

func baseConfig() Config {
	return Config{
		Enabled:         true,
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
		Region:          "cn-shanghai",
		Timeout:         5 * time.Second,
	}
}

func TestDefaultConfigAndNormalize(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Fatal("disabled by default")
	}
	c.Region = ""
	c.Timeout = 0
	n := c.normalize()
	if n.Region != "cn-hangzhou" || n.Timeout != 10*time.Second {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	c := baseConfig()
	c.AccessKeyID = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "access_key") {
		t.Fatalf("missing keys: %v", err)
	}

	c = baseConfig()
	c.SMS.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sign_name") {
		t.Fatalf("missing sign: %v", err)
	}

	c = baseConfig()
	c.Voice.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "called_show_number") {
		t.Fatalf("missing show number: %v", err)
	}

	c = baseConfig()
	c.Storage.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("missing storage: %v", err)
	}
}

func TestEndpoints(t *testing.T) {
	c := baseConfig()
	if got := c.smsEndpoint(); got != "dysmsapi.cn-shanghai.aliyuncs.com" {
		t.Fatalf("sms endpoint = %s", got)
	}
	if got := c.voiceEndpoint(); got != "dyvmsapi.cn-shanghai.aliyuncs.com" {
		t.Fatalf("voice endpoint = %s", got)
	}
	c.SMS.Endpoint = "dysmsapi.aliyuncs.com"
	c.Voice.Endpoint = "dyvmsapi.aliyuncs.com"
	if got := c.smsEndpoint(); got != "dysmsapi.aliyuncs.com" {
		t.Fatalf("override sms endpoint = %s", got)
	}
	if got := c.voiceEndpoint(); got != "dyvmsapi.aliyuncs.com" {
		t.Fatalf("override voice endpoint = %s", got)
	}
}

func TestNewClientsOffline(t *testing.T) {
	c := baseConfig()
	c.SMS.SignName = "签名"
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	if sms == nil {
		t.Fatal("nil sms")
	}
	c.Voice.CalledShowNumber = "057100000000"
	if _, err := NewVoice(c); err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	c.Storage.Endpoint = "https://oss-cn-shanghai.aliyuncs.com"
	c.Storage.Bucket = "demo"
	if _, err := NewStorage(c); err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
}

func TestSmsProviderMetadataContract(t *testing.T) {
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SignName = "签名"
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	// 缺少 template_code 时应在网络调用前报错。
	err = provider.Send(context.Background(), []string{"13800000000"}, "content", map[string]string{"name": "x"})
	if err == nil || !strings.Contains(err.Error(), "template_code") {
		t.Fatalf("missing template_code: %v", err)
	}
}

func TestStartupDisabled(t *testing.T) {
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if GetSms() != nil || GetVoice() != nil || GetStorage() != nil {
		t.Fatal("expected nil clients when disabled")
	}
}
