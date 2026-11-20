package onenet

// 本文件通过 httptest 补齐 onenet 短信客户端的错误分支与 notify 适配路径。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSmsAt 返回端点指向本地模拟服务的短信客户端。
func newSmsAt(t *testing.T, handler http.HandlerFunc) *Sms {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Endpoint = srv.URL
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	return sms
}

func TestNewSmsInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SIcode = ""
	if _, err := NewSms(c); err == nil || !strings.Contains(err.Error(), "sicode") {
		t.Fatalf("expected sicode error: %v", err)
	}
}

func TestSmsSendContextCanceled(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sms.Send(ctx, []string{"138"}, "s", "t", nil); err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestSmsSendMissingSign(t *testing.T) {
	c := baseConfig()
	// 关闭短信子服务校验，仅测试 Send 内部签名回退为空时的报错。
	c.SMS = SMSConfig{Enabled: false, SignID: ""}
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	if _, err := sms.Send(context.Background(), []string{"138"}, "  ", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "sign_id") {
		t.Fatalf("expected sign_id error: %v", err)
	}
}

func TestSmsSendBadEndpoint(t *testing.T) {
	c := baseConfig()
	// 主机名含空格，http.NewRequestWithContext 解析 URL 失败。
	c.Endpoint = "http://exa mple.com"
	sms, err := NewSms(c)
	if err != nil {
		t.Fatalf("NewSms: %v", err)
	}
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "build sms request") {
		t.Fatalf("expected build request error: %v", err)
	}
}

func TestSmsSendTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 服务端已关闭，请求必然连接失败。
	c := baseConfig()
	c.Endpoint = url
	sms, _ := NewSms(c)
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "send sms") {
		t.Fatalf("expected transport error: %v", err)
	}
}

func TestSmsSendReadBodyError(t *testing.T) {
	// 声明的 Content-Length 大于实际写入字节数，响应被截断，读取响应体失败。
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "128")
		_, _ = w.Write([]byte(`{"result":"11101"`))
	})
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "read sms response") {
		t.Fatalf("expected read body error: %v", err)
	}
}

func TestSmsSendHTTPStatusError(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "http 500") {
		t.Fatalf("expected http status error: %v", err)
	}
}

func TestSmsSendBadJSON(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	if _, err := sms.Send(context.Background(), []string{"138"}, "s", "t", nil); err == nil ||
		!strings.Contains(err.Error(), "decode sms response") {
		t.Fatalf("expected decode error: %v", err)
	}
}

func TestSendSmsNilRequest(t *testing.T) {
	sms := newSmsAt(t, func(w http.ResponseWriter, r *http.Request) {})
	if err := sms.SendSms(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "request is nil") {
		t.Fatalf("expected nil request error: %v", err)
	}
}

func TestNewSmsProviderInvalidConfig(t *testing.T) {
	c := baseConfig()
	c.SMS.Enabled = true
	c.SMS.SignID = ""
	if _, err := NewSmsProvider(c); err == nil || !strings.Contains(err.Error(), "sign_id") {
		t.Fatalf("expected sign_id error: %v", err)
	}
}

func TestSmsProviderSendSuccess(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"result":"11121","resultInfo":"serial-p","errorInfo":""}`))
	}))
	t.Cleanup(srv.Close)
	c := baseConfig()
	c.Endpoint = srv.URL
	provider, err := NewSmsProvider(c)
	if err != nil {
		t.Fatalf("NewSmsProvider: %v", err)
	}
	// template_id/sign_id 作为元数据键被提取，其余键作为模板参数。
	err = provider.Send(context.Background(), []string{"138"}, "content", map[string]string{
		"template_id": "tpl-9",
		"sign_id":     "sign-9",
		"code":        "1234",
	})
	if err != nil {
		t.Fatalf("provider send: %v", err)
	}
	for _, want := range []string{"tempid=tpl-9", "signId=sign-9", "code=1234"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
}
