package ctyun

// 本文件通过 httptest 补齐天翼云短信客户端的错误分支与统一接口/notify 适配路径。
// 说明：sms.go 中两处 json.Marshal(map[string]string) 的错误返回分支
// 逻辑上不可达（字符串 map 序列化恒成功），属于防御性代码，无法在测试中触发。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/god-jason/goframe/cloud"
)

// newSmsAt 返回端点指向本地模拟服务的短信客户端。
func newSmsAt(t *testing.T, handler http.HandlerFunc) *Sms {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Endpoint = srv.URL
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	return sms
}

func TestNewSmsInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.AccessKeyID = ""
	if _, err := NewSms(c); err == nil || !strings.Contains(err.Error(), "access_key_id") {
		t.Fatalf("expected keys error: %v", err)
	}
}

func TestSmsSendContextCanceled(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sms.Send(ctx, []string{"138"}, "s", "t", nil); err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestSmsSendBadEndpoint(t *testing.T) {
	c := baseConfig()
	// 主机名含空格，http.NewRequestWithContext 解析 URL 失败。
	c.SMS.Endpoint = "http://exa mple.com"
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "build sms request") {
		t.Fatalf("expected build request error: %v", err)
	}
}

func TestSmsSendTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 服务端已关闭，请求必然连接失败。
	c := baseConfig()
	c.SMS.Endpoint = url
	sms, _ := NewSms(c)
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "send sms") {
		t.Fatalf("expected transport error: %v", err)
	}
}

func TestSmsSendReadBodyError(t *testing.T) {
	// 声明的 Content-Length 大于实际写入字节数，响应被截断，读取响应体失败。
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "128")
		_, _ = w.Write([]byte(`{"code":"OK"`))
	})
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "read sms response") {
		t.Fatalf("expected read body error: %v", err)
	}
}

func TestSmsSendHTTPStatusError(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "http 500") {
		t.Fatalf("expected http status error: %v", err)
	}
}

func TestSmsSendBadJSON(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	if err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "decode sms response") {
		t.Fatalf("expected decode error: %v", err)
	}
}

func TestSendSmsNilAndSuccess(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"OK","message":"成功","requestId":"r-1"}`))
	})
	if err := sms.SendSms(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "request is nil") {
		t.Fatalf("expected nil request error: %v", err)
	}
	err := sms.SendSms(context.Background(), &cloud.SmsRequest{
		PhoneNumbers: []string{"13800000000"},
		TemplateID:   "TC",
		Params:       map[string]string{"code": "1234"},
	})
	if err != nil {
		t.Fatalf("SendSms: %v", err)
	}
}

func TestNewSmsProviderInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SMS.SignName = ""
	if _, err := NewSmsProvider(c); err == nil || !strings.Contains(err.Error(), "sign_name") {
		t.Fatalf("expected sign_name error: %v", err)
	}
}

func TestSmsProviderSendSuccess(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		_, _ = w.Write([]byte(`{"code":"OK","message":"成功","requestId":"r-1"}`))
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.SMS.Endpoint = srv.URL
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	// template_code 作为元数据键被提取，其余键作为模板变量。
	err = provider.Send(context.Background(), []string{"13800000000", "13900000000"}, "content",
		map[string]string{"template_code": "TC", "code": "1234"})
	if err != nil {
		t.Fatalf("provider send: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 per-phone requests, got %d", count)
	}
}
