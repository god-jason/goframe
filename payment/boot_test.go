package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
)

// paymentEnvKeys 是支付模块所有可被环境变量覆盖的配置键。
var paymentEnvKeys = []string{
	"GOFRAME_PAYMENT_ENABLED",
	"GOFRAME_PAYMENT_NOTIFY_PATH",
	"GOFRAME_PAYMENT_TIMEOUT",
	"GOFRAME_PAYMENT_IDEMPOTENCY_DONE_TTL",
	"GOFRAME_PAYMENT_IDEMPOTENCY_PROCESSING_TTL",
	"GOFRAME_PAYMENT_WECHAT_ENABLED",
	"GOFRAME_PAYMENT_WECHAT_APP_ID",
	"GOFRAME_PAYMENT_WECHAT_MCH_ID",
	"GOFRAME_PAYMENT_WECHAT_SERIAL_NO",
	"GOFRAME_PAYMENT_WECHAT_PRIVATE_KEY",
	"GOFRAME_PAYMENT_WECHAT_APIV3_KEY",
	"GOFRAME_PAYMENT_WECHAT_NOTIFY_URL",
	"GOFRAME_PAYMENT_ALIPAY_ENABLED",
	"GOFRAME_PAYMENT_ALIPAY_APP_ID",
	"GOFRAME_PAYMENT_ALIPAY_PRIVATE_KEY",
	"GOFRAME_PAYMENT_ALIPAY_PUBLIC_KEY",
	"GOFRAME_PAYMENT_ALIPAY_PRODUCTION",
	"GOFRAME_PAYMENT_ALIPAY_NOTIFY_URL",
	"GOFRAME_PAYMENT_ALIPAY_RETURN_URL",
}

// setPaymentEnv 设置支付配置环境变量，并在测试结束时恢复原值。
func setPaymentEnv(t *testing.T, values map[string]string) {
	t.Helper()
	previous := make(map[string]*string, len(paymentEnvKeys))
	for _, key := range paymentEnvKeys {
		if v, ok := os.LookupEnv(key); ok {
			vv := v
			previous[key] = &vv
		} else {
			previous[key] = nil
		}
	}
	for k, v := range values {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("config.Reload: %v", err)
	}
	t.Cleanup(func() {
		for k, v := range previous {
			if v == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *v)
			}
		}
		_ = config.Reload()
	})
}

// TestStartupDisabled 验证未启用时 Startup 直接返回 nil，Get 返回 nil。
func TestStartupDisabled(t *testing.T) {
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED": "false",
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled Startup must leave manager nil")
	}
}

// TestStartupValidateError 启用但无渠道 → Validate 报错。
func TestStartupValidateError(t *testing.T) {
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED": "true",
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("Startup without channels must fail validation")
	}
}

// TestStartupWechatBuildFailure 微信渠道构建失败 → loadKeyMaterial 文件不存在 → 错误回传。
func TestStartupWechatBuildFailure(t *testing.T) {
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":             "true",
		"GOFRAME_PAYMENT_WECHAT_ENABLED":      "true",
		"GOFRAME_PAYMENT_WECHAT_APP_ID":       "wx1",
		"GOFRAME_PAYMENT_WECHAT_MCH_ID":       "mch1",
		"GOFRAME_PAYMENT_WECHAT_SERIAL_NO":    "SN1",
		"GOFRAME_PAYMENT_WECHAT_PRIVATE_KEY":  filepath.Join(t.TempDir(), "missing.pem"),
		"GOFRAME_PAYMENT_WECHAT_APIV3_KEY":    "0123456789abcdef0123456789abcdef",
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("Startup with missing wechat key file must fail")
	}
}

// TestStartupAlipayBuildFailure 支付宝渠道构建失败 → LoadAliPayPublicKey 报错。
func TestStartupAlipayBuildFailure(t *testing.T) {
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":              "true",
		"GOFRAME_PAYMENT_ALIPAY_ENABLED":       "true",
		"GOFRAME_PAYMENT_ALIPAY_APP_ID":        "2021000000000001",
		"GOFRAME_PAYMENT_ALIPAY_PRIVATE_KEY":   "-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----",
		"GOFRAME_PAYMENT_ALIPAY_PUBLIC_KEY":    "-----BEGIN PUBLIC KEY-----\nZm9v\n-----END PUBLIC KEY-----",
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("Startup with bad alipay public key must fail")
	}
}

// TestStartupFullSuccess 同时启用微信与支付宝，走完整启动与关闭路径。
func TestStartupFullSuccess(t *testing.T) {
	dir := t.TempDir()
	// 微信私钥写文件（覆盖 loadKeyMaterial 文件读取分支）。
	material := newWechatTestMaterial(t)
	wechatKeyPath := filepath.Join(dir, "wechat.pem")
	if err := os.WriteFile(wechatKeyPath, []byte(material.merchantPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	// 支付宝密钥写文件。
	alipayPriv, alipayPub, _ := generateAlipayKeys(t)
	alipayPrivPath := filepath.Join(dir, "alipay-priv.pem")
	alipayPubPath := filepath.Join(dir, "alipay-pub.pem")
	if err := os.WriteFile(alipayPrivPath, []byte(alipayPriv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alipayPubPath, []byte(alipayPub), 0o600); err != nil {
		t.Fatal(err)
	}

	// 微信证书服务器 + 传输层覆盖（与 startWechatTestEnv 同源，使用 pristineTransport 避免嵌套劫持）。
	server := newHTTPTestServer(t, wechatSuccessHandler(material, t))
	installRewriteTransport(t, server)

	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":                        "true",
		"GOFRAME_PAYMENT_NOTIFY_PATH":                    "/pay/notify",
		"GOFRAME_PAYMENT_TIMEOUT":                        "5s",
		"GOFRAME_PAYMENT_IDEMPOTENCY_DONE_TTL":           "48h",
		"GOFRAME_PAYMENT_IDEMPOTENCY_PROCESSING_TTL":     "30s",
		"GOFRAME_PAYMENT_WECHAT_ENABLED":                 "true",
		"GOFRAME_PAYMENT_WECHAT_APP_ID":                  "wxtestappid001",
		"GOFRAME_PAYMENT_WECHAT_MCH_ID":                  "1900000109",
		"GOFRAME_PAYMENT_WECHAT_SERIAL_NO":               "MCHTESTSERIAL",
		"GOFRAME_PAYMENT_WECHAT_PRIVATE_KEY":             wechatKeyPath,
		"GOFRAME_PAYMENT_WECHAT_APIV3_KEY":               material.apiV3Key,
		"GOFRAME_PAYMENT_WECHAT_NOTIFY_URL":              "https://merchant.example.com/notify",
		"GOFRAME_PAYMENT_ALIPAY_ENABLED":                 "true",
		"GOFRAME_PAYMENT_ALIPAY_APP_ID":                  "2021000000000001",
		"GOFRAME_PAYMENT_ALIPAY_PRIVATE_KEY":             alipayPrivPath,
		"GOFRAME_PAYMENT_ALIPAY_PUBLIC_KEY":              alipayPubPath,
	})

	api.Reset()
	t.Cleanup(api.Reset)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("manager must be set after Startup")
	}
	if Get().Wechat() == nil || Get().Alipay() == nil {
		t.Fatal("both channels must be registered")
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("manager must be cleared after Shutdown")
	}
}

// TestStartupWithRedisCache 验证 cache 为 RedisCache 时走分布式锁分支。
func TestStartupWithRedisCache(t *testing.T) {
	server := miniredis.RunT(t)
	defer server.Close()

	// 先启动 cache，使 cache.Get() 返回 *RedisCache。
	setRedisEnv(t, map[string]string{
		"GOFRAME_REDIS_ENABLED":    "true",
		"GOFRAME_REDIS_ADDR":       server.Addr(),
		"GOFRAME_REDIS_KEY_PREFIX": "boot:",
	})
	if err := cache.Startup(context.Background()); err != nil {
		t.Fatalf("cache.Startup: %v", err)
	}
	t.Cleanup(func() { _ = cache.Shutdown(context.Background()) })

	// 仅启用支付宝（避免微信证书下载网络）。
	dir := t.TempDir()
	alipayPriv, alipayPub, _ := generateAlipayKeys(t)
	alipayPrivPath := filepath.Join(dir, "alipay-priv.pem")
	alipayPubPath := filepath.Join(dir, "alipay-pub.pem")
	if err := os.WriteFile(alipayPrivPath, []byte(alipayPriv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alipayPubPath, []byte(alipayPub), 0o600); err != nil {
		t.Fatal(err)
	}
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":          "true",
		"GOFRAME_PAYMENT_ALIPAY_ENABLED":   "true",
		"GOFRAME_PAYMENT_ALIPAY_APP_ID":        "2021000000000001",
		"GOFRAME_PAYMENT_ALIPAY_PRIVATE_KEY":   alipayPrivPath,
		"GOFRAME_PAYMENT_ALIPAY_PUBLIC_KEY":    alipayPubPath,
	})

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("manager must be set")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// setRedisEnv 设置缓存环境变量（与 cache/boot_test.go 一致）。
func setRedisEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{"GOFRAME_REDIS_ENABLED", "GOFRAME_REDIS_ADDR", "GOFRAME_REDIS_KEY_PREFIX"}
	previous := make(map[string]*string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			previous[k] = &vv
		} else {
			previous[k] = nil
		}
	}
	for k, v := range values {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("config.Reload: %v", err)
	}
	t.Cleanup(func() {
		for k, v := range previous {
			if v == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *v)
			}
		}
		_ = config.Reload()
	})
}

// newHTTPTestServer 启动 httptest.Server 并在测试结束时关闭。
func newHTTPTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// installRewriteTransport 把发往网关的请求重写到本地服务器，测试结束时恢复。
func installRewriteTransport(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = &rewriteTransport{target: server, orig: pristineTransport}
	t.Cleanup(func() { http.DefaultTransport = original })
}

// TestStartupRegisterRoutes 验证 Startup 内部 register 登记了回调路由。
func TestStartupRegisterRoutes(t *testing.T) {
	// 仅启用支付宝（避免微信证书下载网络）。
	dir := t.TempDir()
	alipayPriv, alipayPub, _ := generateAlipayKeys(t)
	alipayPrivPath := filepath.Join(dir, "alipay-priv.pem")
	alipayPubPath := filepath.Join(dir, "alipay-pub.pem")
	if err := os.WriteFile(alipayPrivPath, []byte(alipayPriv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alipayPubPath, []byte(alipayPub), 0o600); err != nil {
		t.Fatal(err)
	}
	setPaymentEnv(t, map[string]string{
		"GOFRAME_PAYMENT_ENABLED":              "true",
		"GOFRAME_PAYMENT_NOTIFY_PATH":          "/pay/notify",
		"GOFRAME_PAYMENT_ALIPAY_ENABLED":       "true",
		"GOFRAME_PAYMENT_ALIPAY_APP_ID":        "2021000000000001",
		"GOFRAME_PAYMENT_ALIPAY_PRIVATE_KEY":   alipayPrivPath,
		"GOFRAME_PAYMENT_ALIPAY_PUBLIC_KEY":    alipayPubPath,
	})
	api.Reset()
	t.Cleanup(api.Reset)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(context.Background()) }()

	found := false
	for _, entry := range api.Entries() {
		if entry.Path == "/pay/notify/alipay" {
			found = true
		}
	}
	if !found {
		t.Fatal("Startup must register alipay notify route")
	}
}

// TestGetConfig 验证 GetConfig 返回默认配置（覆盖 config.go:101）。
func TestGetConfig(t *testing.T) {
	cfg := GetConfig()
	if cfg == nil {
		t.Fatal("GetConfig must not return nil")
	}
	if cfg.Timeout != 10*time.Second {
		t.Fatalf("default timeout = %v", cfg.Timeout)
	}
}
