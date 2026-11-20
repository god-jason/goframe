package tencent

// 本文件覆盖 boot.Startup 的启用与失败路径：
// 通过临时 YAML 配置文件 + config.Reload 注入 tencent 配置段，
// 各客户端构造不产生真实网络请求，端点仅指向本地占位地址。

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
	path := filepath.Join(t.TempDir(), "tencent-config.yaml")
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

func TestStartupEnabledAll(t *testing.T) {
	setConfigYAML(t, `
tencent:
  enabled: true
  secret_id: id
  secret_key: key
  region: ap-shanghai
  timeout: 5s
  sms:
    enabled: true
    sdk_app_id: "1200000000"
    sign_name: 签名
  voice:
    enabled: true
    sdk_app_id: "1400000000"
  storage:
    enabled: true
    bucket: demo-1250000000
    endpoint: http://127.0.0.1:1
`)
	// 结束后清空全局实例，避免影响其他测试对未启用状态的断言。
	t.Cleanup(func() {
		global.mu.Lock()
		global.sms, global.voice, global.storage = nil, nil, nil
		global.mu.Unlock()
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if GetSms() == nil || GetVoice() == nil || GetStorage() == nil {
		t.Fatal("expected all clients initialized")
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

func TestStartupErrorBranches(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "sms missing sdk_app_id",
			body: `
tencent:
  enabled: true
  secret_id: id
  secret_key: key
  sms:
    enabled: true
`,
			wantErr: "sdk_app_id",
		},
		{
			name: "voice missing sdk_app_id",
			body: `
tencent:
  enabled: true
  secret_id: id
  secret_key: key
  voice:
    enabled: true
`,
			wantErr: "voice.sdk_app_id",
		},
		{
			name: "storage missing bucket",
			body: `
tencent:
  enabled: true
  secret_id: id
  secret_key: key
  storage:
    enabled: true
`,
			wantErr: "storage.bucket",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigYAML(t, tt.body)
			err := Startup(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("startup error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}
