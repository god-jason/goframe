package ctyun

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func baseConfig() Config {
	return Config{
		Enabled:         true,
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		Timeout:         5 * time.Second,
		SMS:             SMSConfig{Enabled: true, SignName: "签名", Endpoint: DefaultSMSEndpoint},
	}
}

func TestDefaultConfigAndNormalize(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Fatal("disabled by default")
	}
	c.Timeout = 0
	c.SMS.Endpoint = ""
	n := c.normalize()
	if n.Timeout != 10*time.Second || n.SMS.Endpoint != DefaultSMSEndpoint {
		t.Fatalf("normalize = %+v", n)
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	c := baseConfig()
	c.AccessKeyID = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "access_key_id") {
		t.Fatalf("missing keys: %v", err)
	}
	c = baseConfig()
	c.SMS.SignName = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sign_name") {
		t.Fatalf("missing sign: %v", err)
	}
	c = baseConfig()
	c.Storage.Enabled = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("missing storage: %v", err)
	}
}

func TestEOpSignStable(t *testing.T) {
	u, _ := url.Parse("https://sms-global.ctapi.ctyun.cn/sms/api/v1?b=2&a=1")
	body := []byte(`{"action":"SendSms"}`)
	now := time.Unix(1700000000, 0).UTC()
	d1, a1 := eopSign("ak", "sk", "req-1", u, body, now)
	d2, a2 := eopSign("ak", "sk", "req-1", u, body, now)
	if d1 != d2 || a1 != a2 {
		t.Fatal("signature not deterministic")
	}
	if d1 != now.Format(eopTimeLayout) {
		t.Fatalf("eop-date = %s", d1)
	}
	if !strings.HasPrefix(a1, "ak Headers=ctyun-eop-request-id;eop-date Signature=") {
		t.Fatalf("authorization = %s", a1)
	}
	// 请求体不同应产生不同签名。
	_, a3 := eopSign("ak", "sk", "req-1", u, []byte(`{"action":"Other"}`), now)
	if a3 == a1 {
		t.Fatal("signature should change with body")
	}
}

func TestSortedQuery(t *testing.T) {
	u, _ := url.Parse("https://x/y?c=3&a=1&b=2")
	if got := sortedQuery(u); got != "a=1&b=2&c=3" {
		t.Fatalf("sorted query = %q", got)
	}
	if got := sortedQuery(nil); got != "" {
		t.Fatalf("nil query = %q", got)
	}
}

func TestSplitEndpoint(t *testing.T) {
	if h, s := splitEndpoint("https://eos.example.cn", true); h != "eos.example.cn" || !s {
		t.Fatalf("https = %s %v", h, s)
	}
	if h, s := splitEndpoint("http://eos.example.cn", true); h != "eos.example.cn" || s {
		t.Fatalf("http = %s %v", h, s)
	}
	if h, s := splitEndpoint("eos.example.cn", false); h != "eos.example.cn" || s {
		t.Fatalf("bare = %s %v", h, s)
	}
}

func TestSmsSendSuccess(t *testing.T) {
	var gotAuth, gotReqID, gotDate string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Eop-Authorization")
		gotReqID = r.Header.Get("ctyun-eop-request-id")
		gotDate = r.Header.Get("Eop-date")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"OK","message":"成功","requestId":"r-1"}`))
	}))
	defer srv.Close()

	c := baseConfig()
	c.SMS.Endpoint = srv.URL
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	err = sms.Send(context.Background(), []string{"13800000000", "13900000000"}, "", "SMS_CODE", map[string]string{"code": "8888"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotBody["action"] != "SendSms" || gotBody["templateCode"] != "SMS_CODE" || gotBody["signName"] != "签名" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["phoneNumber"] != "13900000000" {
		t.Fatalf("last phone = %s", gotBody["phoneNumber"])
	}
	if !strings.Contains(gotBody["templateParam"], "8888") {
		t.Fatalf("templateParam = %s", gotBody["templateParam"])
	}
	if gotAuth == "" || gotReqID == "" || gotDate == "" {
		t.Fatalf("missing eop headers: auth=%q id=%q date=%q", gotAuth, gotReqID, gotDate)
	}
}

func TestSmsSendFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":30021,"message":"签名错误","requestId":"r-1"}`))
	}))
	defer srv.Close()
	c := baseConfig()
	c.SMS.Endpoint = srv.URL
	sms, _ := NewSms(c)
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil {
		t.Fatal("expected failure")
	}
}

func TestSmsSendValidation(t *testing.T) {
	sms, _ := NewSms(baseConfig())
	if err := sms.Send(context.Background(), nil, "s", "t", nil); err == nil {
		t.Fatal("expected empty phone error")
	}
	if err := sms.Send(context.Background(), []string{"138"}, "s", "", nil); err == nil {
		t.Fatal("expected empty template error")
	}
}

func TestSmsProviderMetadataContract(t *testing.T) {
	c := baseConfig()
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	err = provider.Send(context.Background(), []string{"138"}, "content", map[string]string{"name": "x"})
	if err == nil || !strings.Contains(err.Error(), "template_code") {
		t.Fatalf("missing template_code: %v", err)
	}
}

func TestStartupDisabled(t *testing.T) {
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if GetSms() != nil || GetStorage() != nil {
		t.Fatal("expected nil clients when disabled")
	}
}
