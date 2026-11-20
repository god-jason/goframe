package tencent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func baseConfig() Config {
	return Config{
		Enabled:   true,
		SecretID:  "id",
		SecretKey: "key",
		Region:    "ap-shanghai",
		Timeout:   5 * time.Second,
	}
}

func TestDefaultConfigAndNormalize(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled || c.Region != "ap-guangzhou" || c.Timeout != 10*time.Second {
		t.Fatalf("defaults = %+v", c)
	}
	c.Region = ""
	c.Timeout = -1
	c.Voice.PlayTimes = -3
	n := c.normalize()
	if n.Region != "ap-guangzhou" || n.Timeout != 10*time.Second || n.Voice.PlayTimes != 0 {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	c := baseConfig()
	c.SecretKey = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("missing secret: %v", err)
	}

	c = baseConfig()
	c.SMS.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sdk_app_id") {
		t.Fatalf("sms: %v", err)
	}

	c = baseConfig()
	c.Voice.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "voice.sdk_app_id") {
		t.Fatalf("voice: %v", err)
	}

	c = baseConfig()
	c.Storage.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "storage.bucket") {
		t.Fatalf("storage: %v", err)
	}
}

func TestBucketURL(t *testing.T) {
	c := baseConfig()
	c.Storage.Bucket = "demo-1250000000"
	if got := c.bucketURL(); got != "https://demo-1250000000.cos.ap-shanghai.myqcloud.com" {
		t.Fatalf("bucketURL = %s", got)
	}
	c.Storage.Region = "ap-beijing"
	if got := c.bucketURL(); got != "https://demo-1250000000.cos.ap-beijing.myqcloud.com" {
		t.Fatalf("region override = %s", got)
	}
	c.Storage.Endpoint = "https://custom.example.com"
	if got := c.bucketURL(); got != "https://custom.example.com" {
		t.Fatalf("endpoint override = %s", got)
	}
}

func TestNewClientsOffline(t *testing.T) {
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SdkAppID = "1200000000"
	c.SMS.SignName = "签名"
	if _, err := NewSms(c); err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	c.Voice.Enabled = true
	c.Voice.SdkAppID = "1200000000"
	if _, err := NewVoice(c); err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	c.Storage.Enabled = true
	c.Storage.Bucket = "demo-1250000000"
	if _, err := NewStorage(c); err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
}

func TestSmsProviderMetadataContract(t *testing.T) {
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SdkAppID = "1200000000"
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	err = provider.Send(context.Background(), []string{"+8613800000000"}, "content", map[string]string{"template_params": "1,2"})
	if err == nil || !strings.Contains(err.Error(), "template_id") {
		t.Fatalf("missing template_id: %v", err)
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
