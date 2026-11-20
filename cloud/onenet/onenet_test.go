package onenet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/cloud"
)

func baseConfig() Config {
	return Config{
		Enabled:  true,
		SIcode:   "si-123",
		Endpoint: "http://api.sms.heclouds.com",
		Timeout:  5 * time.Second,
		SMS:      SMSConfig{Enabled: true, SignID: "sign-1"},
	}
}

func TestDefaultConfigAndNormalize(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Fatal("disabled by default")
	}
	c.Endpoint = ""
	c.Timeout = 0
	n := c.normalize()
	if n.Endpoint != DefaultEndpoint || n.Timeout != 10*time.Second {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	c := baseConfig()
	c.SIcode = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sicode") {
		t.Fatalf("missing sicode: %v", err)
	}
	c = baseConfig()
	c.SMS.SignID = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sign_id") {
		t.Fatalf("missing sign_id: %v", err)
	}
}

func TestSmsSendSuccess(t *testing.T) {
	var gotQuery string
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
		w.Header().Set("Content-Type", "text/plain;charset=utf-8")
		_, _ = w.Write([]byte(`{"result":"11101","resultInfo":"serial-9","errorInfo":""}`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Endpoint = srv.URL
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	seq, err := sms.Send(context.Background(), []string{"13800000000", "13900000000"}, "", "tpl-1", map[string]string{"code": "8888"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if seq != "serial-9" {
		t.Fatalf("seq = %q", seq)
	}
	if gotPath != "/tempSignSmsSend" || gotMethod != http.MethodPost {
		t.Fatalf("path=%s method=%s", gotPath, gotMethod)
	}
	for _, want := range []string{"sicode=si-123", "tempid=tpl-1", "signId=sign-1", "code=8888"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
	if !strings.Contains(gotQuery, "mobiles=13800000000%2C13900000000") {
		t.Fatalf("mobiles not joined: %q", gotQuery)
	}
}

func TestSmsSendPartialAndFailure(t *testing.T) {
	var result string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"` + result + `","resultInfo":"info","errorInfo":"bad"}`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.Endpoint = srv.URL
	sms, _ := NewSms(c)

	// 部分成功视为成功。
	result = "11107"
	if _, err := sms.Send(context.Background(), []string{"13800000000"}, "s", "t", nil); err != nil {
		t.Fatalf("partial should succeed: %v", err)
	}
	// 明确失败码返回错误。
	result = "11102"
	if _, err := sms.Send(context.Background(), []string{"13800000000"}, "s", "t", nil); err == nil {
		t.Fatal("expected failure")
	}
}

func TestSmsSendValidation(t *testing.T) {
	sms, _ := NewSms(baseConfig())
	if _, err := sms.Send(context.Background(), nil, "s", "t", nil); err == nil {
		t.Fatal("expected empty phone error")
	}
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "", nil); err == nil {
		t.Fatal("expected empty template error")
	}
}

func TestSendSmsUnified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"11101","resultInfo":"x","errorInfo":""}`))
	}))
	defer srv.Close()
	c := baseConfig()
	c.Endpoint = srv.URL
	sms, _ := NewSms(c)
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"13800000000"},
		TemplateID:   "tpl-1",
		Params:       map[string]string{"code": "8888"},
	})
	if err != nil {
		t.Fatalf("SendSms: %v", err)
	}
}

func TestSmsProviderMetadataContract(t *testing.T) {
	c := baseConfig()
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	err = provider.Send(context.Background(), []string{"138"}, "content", map[string]string{"code": "1"})
	if err == nil || !strings.Contains(err.Error(), "template_id") {
		t.Fatalf("missing template_id: %v", err)
	}
}

func TestStartupDisabled(t *testing.T) {
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if GetSms() != nil {
		t.Fatal("expected nil sms when disabled")
	}
}
