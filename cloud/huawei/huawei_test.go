package huawei

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
		SecretAccessKey: "sk",
		ProjectID:       "project",
		Region:          "cn-east-3",
		Timeout:         5 * time.Second,
	}
}

func TestDefaultConfigAndNormalize(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled || c.Region != "cn-north-4" || c.Timeout != 10*time.Second {
		t.Fatalf("defaults = %+v", c)
	}
	c.Region = ""
	c.Timeout = -1
	c.Voice.CountryCode = ""
	c.Voice.PlayTimes = -2
	n := c.normalize()
	if n.Region != "cn-north-4" || n.Timeout != 10*time.Second || n.Voice.CountryCode != "CN" || n.Voice.PlayTimes != 0 {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	c := baseConfig()
	c.SecretAccessKey = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "secret_access_key") {
		t.Fatalf("missing sk: %v", err)
	}

	c = baseConfig()
	c.SMS.Enabled = true
	c.ProjectID = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("sms: %v", err)
	}

	c = baseConfig()
	c.Voice.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "show_number") {
		t.Fatalf("voice: %v", err)
	}

	c = baseConfig()
	c.Storage.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("storage: %v", err)
	}
}

func TestVCPEndpoint(t *testing.T) {
	c := baseConfig()
	if got := c.vcpEndpoint(); got != "https://vcp.cn-east-3.myhuaweicloud.com" {
		t.Fatalf("vcpEndpoint = %s", got)
	}
	c.Voice.Endpoint = "https://vcp.example.com"
	if got := c.vcpEndpoint(); got != "https://vcp.example.com" {
		t.Fatalf("override = %s", got)
	}
}

func TestSmsTemplateVars(t *testing.T) {
	got := smsTemplateVars(map[string]string{
		"template_id": "t",
		"2":           "b",
		"1":           "a",
		"10":          "j",
	})
	if strings.Join(got, ",") != "a,b,j" {
		t.Fatalf("vars = %v", got)
	}
}

func TestNewClientsOffline(t *testing.T) {
	c := baseConfig()
	c.Region = "cn-north-4" // SMSApi 仅支持 cn-north-4 / cn-south-1
	c.SMS.Enabled = true
	c.SMS.Signature = "签名"
	c.SMS.From = "+861000000000"
	if _, err := NewSms(c); err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	c.Voice.Enabled = true
	c.Voice.ShowNumber = "+861000000001"
	if _, err := NewVoice(c); err != nil {
		t.Fatalf("NewVoice: %v", err)
	}
	c.Storage.Enabled = true
	c.Storage.Endpoint = "https://obs.cn-east-3.myhuaweicloud.com"
	c.Storage.Bucket = "demo"
	if _, err := NewStorage(c); err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
}

func TestSmsProviderMetadataContract(t *testing.T) {
	c := baseConfig()
	c.Region = "cn-north-4"
	c.SMS.Enabled = true
	c.SMS.Signature = "签名"
	c.SMS.From = "+861000000000"
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	err = provider.Send(context.Background(), []string{"+8613800000000"}, "content", map[string]string{"1": "a"})
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
