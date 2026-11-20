package payment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

func TestRegisterNotifyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api.Reset()
	defer api.Reset()

	register(Config{Enabled: true, NotifyPath: "/payment/notify"})
	paths := map[string]bool{}
	for _, entry := range api.Entries() {
		if entry.Method == http.MethodPost {
			paths[entry.Method+" "+entry.Path] = true
		}
	}
	for _, want := range []string{"POST /payment/notify/wechat", "POST /payment/notify/alipay"} {
		if !paths[want] {
			t.Fatalf("missing route %q, entries = %v", want, paths)
		}
	}

	api.Reset()
	register(Config{Enabled: false})
	if len(api.Entries()) != 0 {
		t.Fatal("disabled config must not register routes")
	}

	api.Reset()
	register(Config{Enabled: true, NotifyPath: ""})
	found := false
	for _, entry := range api.Entries() {
		if entry.Path == "/payment/notify/wechat" {
			found = true
		}
	}
	if !found {
		t.Fatal("empty notify path must fall back to default")
	}
}

// TestRegisterFromConfig 验证导出的 Register 读取配置并登记路由（http.go:16-17）。
func TestRegisterFromConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api.Reset()
	defer api.Reset()

	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":     "true",
		"GOFRAME_PAYMENT_NOTIFY_PATH": "/cfg/notify",
	})

	Register()
	found := false
	for _, entry := range api.Entries() {
		if entry.Path == "/cfg/notify/wechat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Register must use configured notify path, entries = %d", len(api.Entries()))
	}
}

// errBodyReader 是读取时报错的请求体，用于触发 GetRawData 失败分支。
type errBodyReader struct{}

func (errBodyReader) Read([]byte) (int, error) { return 0, errors.New("read body failed") }

// withGlobalManager 临时替换全局管理器，测试结束后恢复。
func withGlobalManager(t *testing.T, m *Manager) {
	t.Helper()
	global.mu.Lock()
	previous := global.manager
	global.manager = m
	global.mu.Unlock()
	t.Cleanup(func() {
		global.mu.Lock()
		global.manager = previous
		global.mu.Unlock()
	})
}

// TestNotifyHandlerManagerNil 管理器未初始化时按渠道应答失败。
func TestNotifyHandlerManagerNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withGlobalManager(t, nil)

	wechatRouter := gin.New()
	wechatRouter.POST("/n", notifyHandler(ChannelWechat))
	rec := httptest.NewRecorder()
	wechatRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("{}")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("wechat status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "FAIL") {
		t.Fatalf("wechat body = %s", rec.Body.String())
	}

	alipayRouter := gin.New()
	alipayRouter.POST("/n", notifyHandler(ChannelAlipay))
	rec = httptest.NewRecorder()
	alipayRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("x=1")))
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "failure" {
		t.Fatalf("alipay status = %d body = %q", rec.Code, rec.Body.String())
	}
}

// TestNotifyHandlerSuccessAndError 验证回调成功与失败两类应答分支。
func TestNotifyHandlerSuccessAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	clearHandlers()
	t.Cleanup(clearHandlers)
	OnNotify(ChannelWechat, func(context.Context, *NotifyResult) error { return nil })
	OnNotify(ChannelAlipay, func(context.Context, *NotifyResult) error { return nil })

	// 微信成功：200 空响应体。
	success := newTestManager(&fakeChannel{name: ChannelWechat, result: paymentResult()}, newMemStore())
	withGlobalManager(t, success)
	router := gin.New()
	router.POST("/n", notifyHandler(ChannelWechat))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("body")))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("wechat success status = %d body = %q", rec.Code, rec.Body.String())
	}

	// 支付宝成功：200 "success"。
	alipayOK := newTestManager(&fakeChannel{name: ChannelAlipay, result: &NotifyResult{
		Channel: ChannelAlipay, OutTradeNo: "order-1", State: StateSuccess, IdempotencyKey: "ali-1",
	}}, newMemStore())
	withGlobalManager(t, alipayOK)
	router = gin.New()
	router.POST("/n", notifyHandler(ChannelAlipay))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("body")))
	if rec.Code != http.StatusOK || rec.Body.String() != "success" {
		t.Fatalf("alipay success status = %d body = %q", rec.Code, rec.Body.String())
	}

	// 解析失败：微信 500 FAIL。
	broken := newTestManager(&fakeChannel{name: ChannelWechat, parseErr: errors.New("bad sign")}, newMemStore())
	withGlobalManager(t, broken)
	router = gin.New()
	router.POST("/n", notifyHandler(ChannelWechat))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("body")))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "FAIL") {
		t.Fatalf("wechat fail status = %d body = %q", rec.Code, rec.Body.String())
	}

	// 解析失败：支付宝 500 failure。
	brokenAlipay := newTestManager(&fakeChannel{name: ChannelAlipay, parseErr: errors.New("bad sign")}, newMemStore())
	withGlobalManager(t, brokenAlipay)
	router = gin.New()
	router.POST("/n", notifyHandler(ChannelAlipay))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n", strings.NewReader("body")))
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "failure" {
		t.Fatalf("alipay fail status = %d body = %q", rec.Code, rec.Body.String())
	}

	// 请求体读取失败：跳过 body 恢复分支，仍按渠道结果应答。
	fake := &fakeChannel{name: ChannelWechat, result: paymentResult()}
	withGlobalManager(t, newTestManager(fake, newMemStore()))
	router = gin.New()
	router.POST("/n", notifyHandler(ChannelWechat))
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/n", nil)
	req.Body = io.NopCloser(errBodyReader{})
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw data error status = %d", rec.Code)
	}
	if fake.parseCall == 0 {
		t.Fatal("handler must still parse request after raw data error")
	}
}
