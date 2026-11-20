package aliyun

// 本文件通过 httptest 模拟 dysmsapi 服务端，覆盖短信发送与统一接口适配路径。
// 说明：sms.go 中 json.Marshal(map[string]string) 的错误分支逻辑上不可达
// （字符串映射必然编码成功），属于防御性代码，无法在测试中触发。
// NewSms 中 dysmsapi.NewClient 的错误分支同样不可达：
// SDK 仅在 AK/SK 同时非空时构造静态凭证（此时校验必过），
// 且 Endpoint 恒为非空，Init/CheckConfig/GetEndpoint 均不会返回错误。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/god-jason/goframe/cloud"
)

// respondJSON 返回固定 JSON 响应。
func respondJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// smsAt 启动模拟 dysmsapi 的 httptest 服务，返回端点指向它的短信客户端与最近一次请求的查询参数。
// SDK 默认使用 HTTPS，而测试服务是 HTTP，故构造后覆盖 client.Protocol。
func smsAt(t *testing.T, handler http.HandlerFunc) (*Sms, *url.Values) {
	t.Helper()
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SignName = "签名"
	c.SMS.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	sms.client.Protocol = tea.String("HTTP")
	return sms, &query
}

func TestSmsSendOverHTTP(t *testing.T) {
	sms, query := smsAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1"}`))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := sms.Send(ctx, []string{"13800000000", "13900000000"}, "签名", "SMS_1", map[string]string{"code": "8888"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := (*query).Get("PhoneNumbers"); got != "13800000000,13900000000" {
		t.Fatalf("PhoneNumbers = %s", got)
	}
	if got := (*query).Get("TemplateCode"); got != "SMS_1" {
		t.Fatalf("TemplateCode = %s", got)
	}
	if got := (*query).Get("TemplateParam"); !strings.Contains(got, "8888") {
		t.Fatalf("TemplateParam = %s", got)
	}
}

func TestSmsSendWithoutParams(t *testing.T) {
	sms, query := smsAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1"}`))
	if err := sms.Send(context.Background(), []string{"13800000000"}, "签名", "SMS_1", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, ok := (*query)["TemplateParam"]; ok {
		t.Fatalf("unexpected TemplateParam: %v", *query)
	}
}

func TestSmsSendErrors(t *testing.T) {
	// 业务码非 OK。
	sms, _ := smsAt(t, respondJSON(`{"Code":"isv.SMS_SIGNATURE_ILLEGAL","Message":"签名不合法"}`))
	err := sms.Send(context.Background(), []string{"138"}, "签名", "SMS_1", nil)
	if err == nil || !strings.Contains(err.Error(), "isv.SMS_SIGNATURE_ILLEGAL") {
		t.Fatalf("non-OK code: %v", err)
	}

	// HTTP 层错误。
	bad, _ := smsAt(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if err := bad.Send(context.Background(), []string{"138"}, "签名", "SMS_1", nil); err == nil {
		t.Fatal("expected transport error")
	}

	// 参数校验分支（无需网络）。
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sms.Send(cancelled, []string{"138"}, "签名", "SMS_1", nil); err == nil {
		t.Fatal("expected ctx error")
	}
	if err := sms.Send(context.Background(), nil, "签名", "SMS_1", nil); err == nil {
		t.Fatal("expected empty phone error")
	}
	if err := sms.Send(context.Background(), []string{"138"}, "签名", "  ", nil); err == nil {
		t.Fatal("expected empty template error")
	}
}

func TestSmsProviderQueryContract(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1"}`)(w, r)
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SignName = "签名"
	c.SMS.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	provider.sms.client.Protocol = tea.String("HTTP")
	err = provider.Send(context.Background(), []string{"138"}, "content",
		map[string]string{"template_code": "SMS_1", "code": "8888"})
	if err != nil {
		t.Fatalf("provider send: %v", err)
	}
	if query.Get("SignName") != "签名" || query.Get("TemplateCode") != "SMS_1" {
		t.Fatalf("query = %v", query)
	}
	tp := query.Get("TemplateParam")
	if !strings.Contains(tp, "8888") || strings.Contains(tp, "template_code") {
		t.Fatalf("TemplateParam = %s", tp)
	}
}

func TestSendSmsUnified(t *testing.T) {
	sms, query := smsAt(t, respondJSON(`{"Code":"OK","Message":"OK","RequestId":"req-1"}`))

	// nil 请求。
	if err := sms.SendSms(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}

	// Sign 为空时回退到配置签名。
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"138"},
		TemplateID:   "SMS_1",
		Params:       map[string]string{"code": "1"},
	})
	if err != nil {
		t.Fatalf("send sms: %v", err)
	}
	if (*query).Get("SignName") != "签名" {
		t.Fatalf("fallback sign = %s", (*query).Get("SignName"))
	}

	// 显式 Sign。
	if err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"138"},
		Sign:         "其他签名",
		TemplateID:   "SMS_1",
	}); err != nil {
		t.Fatalf("send sms with sign: %v", err)
	}
	if (*query).Get("SignName") != "其他签名" {
		t.Fatalf("explicit sign = %s", (*query).Get("SignName"))
	}

	// 失败路径。
	bad, _ := smsAt(t, respondJSON(`{"Code":"isv.AMOUNT_NOT_ENOUGH","Message":"余额不足"}`))
	if err := bad.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"138"},
		TemplateID:   "SMS_1",
	}); err == nil {
		t.Fatal("expected failure")
	}
}
