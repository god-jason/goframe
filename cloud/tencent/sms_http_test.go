package tencent

// 本文件通过 httptest 模拟腾讯云 SMS API 服务端，覆盖短信发送与统一接口适配路径。
// 说明：sms.go 中 smsapi.NewClient 的错误返回分支逻辑上不可达
// （SDK 构造函数恒返回 nil error），属于防御性代码，无法在测试中触发。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/god-jason/goframe/cloud"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// smsAt 启动模拟 SMS API 的 httptest 服务，返回端点指向它的短信客户端。
// SDK 默认 HTTPS 与真实接入域名，需通过 WithProfile 同时覆盖 Endpoint 与 Scheme。
func smsAt(t *testing.T, handler http.HandlerFunc) *Sms {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SdkAppID = "1200000000"
	c.SMS.SignName = "签名"
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	p.HttpProfile.Scheme = "HTTP"
	sms.client.WithProfile(p)
	return sms
}

// smsOKBody 返回一条发送成功的响应体。
const smsOKBody = `{"Response":{"RequestId":"r-1","SendStatusSet":[{"Code":"Ok","Message":"success","PhoneNumber":"+8613800000000","SerialNo":"s-1"}]}}`

func TestSmsSendOverHTTP(t *testing.T) {
	var gotBody map[string]any
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(smsOKBody))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requestID, err := sms.Send(ctx, []string{"+8613800000000"}, "签名", "ID1", []string{"a", "b"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if requestID != "r-1" {
		t.Fatalf("requestID = %s", requestID)
	}
	if gotBody["SmsSdkAppid"] != "1200000000" || gotBody["TemplateID"] != "ID1" {
		t.Fatalf("body = %+v", gotBody)
	}
	params, _ := gotBody["TemplateParamSet"].([]any)
	if len(params) != 2 || params[0] != "a" {
		t.Fatalf("TemplateParamSet = %v", gotBody["TemplateParamSet"])
	}
}

func TestSmsSendStatusFailure(t *testing.T) {
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"r-2","SendStatusSet":[{"Code":"FailedOperation.SignatureIncorrectOrUnapproved","Message":"签名未审核","PhoneNumber":"+86138","SerialNo":"s-2"}]}}`))
	})
	requestID, err := sms.Send(context.Background(), []string{"+86138"}, "签名", "ID1", nil)
	if err == nil || !strings.Contains(err.Error(), "FailedOperation.SignatureIncorrectOrUnapproved") {
		t.Fatalf("status failure: %v", err)
	}
	if requestID != "r-2" {
		t.Fatalf("requestID = %s", requestID)
	}
}

func TestSmsSendEmptyResponse(t *testing.T) {
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	requestID, err := sms.Send(context.Background(), []string{"+86138"}, "签名", "ID1", nil)
	if err != nil || requestID != "" {
		t.Fatalf("empty response: requestID=%q err=%v", requestID, err)
	}
}

func TestSmsSendErrors(t *testing.T) {
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"InvalidParameterValue","Message":"bad param","RequestId":"r-3"}}}`))
	})
	if _, err := sms.Send(context.Background(), []string{"+86138"}, "签名", "ID1", nil); err == nil {
		t.Fatal("expected sdk error")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sms.Send(cancelled, []string{"+86138"}, "签名", "ID1", nil); err == nil {
		t.Fatal("expected ctx error")
	}
	if _, err := sms.Send(context.Background(), nil, "签名", "ID1", nil); err == nil {
		t.Fatal("expected empty phone error")
	}
	if _, err := sms.Send(context.Background(), []string{"+86138"}, "签名", "  ", nil); err == nil {
		t.Fatal("expected empty template error")
	}
}

func TestSmsProviderSendOverHTTP(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(smsOKBody))
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SdkAppID = "1200000000"
	c.SMS.SignName = "签名"
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	p.HttpProfile.Scheme = "HTTP"
	provider.sms.client.WithProfile(p)

	err = provider.Send(context.Background(), []string{"+86138"}, "content",
		map[string]string{"template_id": "ID1", "template_params": "1,2"})
	if err != nil {
		t.Fatalf("provider send: %v", err)
	}
	if gotBody["Sign"] != "签名" {
		t.Fatalf("Sign = %v", gotBody["Sign"])
	}
	params, _ := gotBody["TemplateParamSet"].([]any)
	if len(params) != 2 {
		t.Fatalf("TemplateParamSet = %v", gotBody["TemplateParamSet"])
	}

	// template_params 为空时不传模板变量。
	err = provider.Send(context.Background(), []string{"+86138"}, "content",
		map[string]string{"template_id": "ID1", "template_params": "  "})
	if err != nil {
		t.Fatalf("provider send without params: %v", err)
	}
}

func TestSendSmsUnified(t *testing.T) {
	var gotBody map[string]any
	sms := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(smsOKBody))
	})

	if err := sms.SendSms(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}

	// Sign 回退配置签名；Params 按键序展开。
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"+86138"},
		TemplateID:   "ID1",
		Params:       map[string]string{"0": "a", "1": "b"},
	})
	if err != nil {
		t.Fatalf("send sms: %v", err)
	}
	if gotBody["Sign"] != "签名" {
		t.Fatalf("fallback sign = %v", gotBody["Sign"])
	}
	params, _ := gotBody["TemplateParamSet"].([]any)
	if len(params) != 2 || params[0] != "a" {
		t.Fatalf("ordered params = %v", gotBody["TemplateParamSet"])
	}

	// TemplateParams 优先于 Params。
	if err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers:   []string{"+86138"},
		Sign:           "其他签名",
		TemplateID:     "ID1",
		Params:         map[string]string{"0": "x"},
		TemplateParams: []string{"y"},
	}); err != nil {
		t.Fatalf("send sms with template params: %v", err)
	}
	if gotBody["Sign"] != "其他签名" {
		t.Fatalf("explicit sign = %v", gotBody["Sign"])
	}
	params, _ = gotBody["TemplateParamSet"].([]any)
	if len(params) != 1 || params[0] != "y" {
		t.Fatalf("template params = %v", gotBody["TemplateParamSet"])
	}

	// 失败路径。
	bad := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"r-9","SendStatusSet":[{"Code":"FailedOperation","Message":"fail"}]}}`))
	})
	if err := bad.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"+86138"},
		TemplateID:   "ID1",
	}); err == nil {
		t.Fatal("expected failure")
	}
}
