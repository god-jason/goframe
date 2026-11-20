package onenet

// 本文件覆盖 boot.Startup 的启用与失败路径：
// 通过临时 YAML 配置文件 + config.Reload 注入 onenet 配置段，
// 客户端构造不产生真实网络请求，端点仅指向本地占位地址。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/notify"
)

// setConfigYAML 写入临时配置文件并重载全局配置，测试结束后恢复默认值。
func setConfigYAML(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "onenet-config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	if err := config.Reload(); err != nil {
		t.Fatalf("config reload: %v", err)
	}
	t.Cleanup(func() {
		config.SetFile("")
		_ = config.Reload()
	})
}

func TestStartupEnabledSMS(t *testing.T) {
	setConfigYAML(t, `
onenet:
  enabled: true
  sicode: si-boot
  endpoint: http://127.0.0.1:1
  sms:
    enabled: true
    sign_id: sign-boot
`)
	// 结束后清空全局实例，避免影响其他测试对未启用状态的断言。
	t.Cleanup(func() {
		global.mu.Lock()
		global.sms = nil
		global.mu.Unlock()
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if GetSms() == nil {
		t.Fatal("expected sms client initialized")
	}
	found := false
	for _, p := range notify.SMSProviders() {
		if _, ok := p.(*SmsProvider); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("sms provider not registered in notify")
	}
}

func TestStartupSMSError(t *testing.T) {
	setConfigYAML(t, `
onenet:
  enabled: true
  sicode: si-boot
  sms:
    enabled: true
`)
	err := Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sign_id") {
		t.Fatalf("startup error = %v, want contains sign_id", err)
	}
}
