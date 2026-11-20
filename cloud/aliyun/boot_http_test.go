package aliyun

// 本文件覆盖 boot.Startup 的启用与失败路径：
// 通过临时 YAML 配置文件 + config.Reload 注入 aliyun 配置段，
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
	path := filepath.Join(t.TempDir(), "aliyun-config.yaml")
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
aliyun:
  enabled: true
  access_key_id: ak
  access_key_secret: sk
  region: cn-hangzhou
  timeout: 5s
  sms:
    enabled: true
    sign_name: 签名
  voice:
    enabled: true
    called_show_number: "057100000000"
  storage:
    enabled: true
    endpoint: http://127.0.0.1:1
    bucket: demo
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
			name: "sms missing sign",
			body: `
aliyun:
  enabled: true
  access_key_id: ak
  access_key_secret: sk
  sms:
    enabled: true
`,
			wantErr: "sign_name",
		},
		{
			name: "voice missing show number",
			body: `
aliyun:
  enabled: true
  access_key_id: ak
  access_key_secret: sk
  voice:
    enabled: true
`,
			wantErr: "called_show_number",
		},
		{
			name: "storage missing bucket",
			body: `
aliyun:
  enabled: true
  access_key_id: ak
  access_key_secret: sk
  storage:
    enabled: true
    endpoint: http://127.0.0.1:1
`,
			wantErr: "storage",
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
