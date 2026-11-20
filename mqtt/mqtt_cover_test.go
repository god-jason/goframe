package mqtt

import (
	"context"
	"os"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/god-jason/goframe/config"
)

// neverToken 实现 paho.Token，任何等待都不完成，用于触发 waitToken 的 ctx 取消分支。
type neverToken struct{}

func (neverToken) Wait() bool                     { return false }
func (neverToken) WaitTimeout(time.Duration) bool { return false }
func (neverToken) Done() <-chan struct{}          { return nil }
func (neverToken) Error() error                   { return nil }

var _ paho.Token = neverToken{}

// TestWaitTokenContextCanceled 覆盖 waitToken 在超时轮询后命中 ctx.Err() 返回错误的分支。
func TestWaitTokenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitToken(ctx, neverToken{}); err == nil {
		t.Fatal("expected context error")
	}
}

// TestStartupTLSSuccess 覆盖 boot.go 中 TLS 配置成功并写入 ClientOptions 的分支。
// 仅开启 TLS 而不提供证书文件，tlscfg.Client 返回默认配置；随后连接不可达 broker 失败。
func TestStartupTLSSuccess(t *testing.T) {
	envs := map[string]string{
		"GOFRAME_MQTT_ENABLED":         "true",
		"GOFRAME_MQTT_BROKERS":         "tcp://127.0.0.1:1",
		"GOFRAME_MQTT_CLIENT_ID":       "tls-cover",
		"GOFRAME_MQTT_CONNECT_TIMEOUT": "200ms",
		"GOFRAME_MQTT_TLS_ENABLED":     "true",
	}
	for k, v := range envs {
		_ = os.Setenv(k, v)
	}
	defer func() {
		for k := range envs {
			_ = os.Unsetenv(k)
		}
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected connect error")
	}
}

// 说明：boot.go 中 Startup 对 New(options) 返回错误的分支不可达——
// New 仅在 Brokers 为空时返回 ErrNoBrokers，而 Startup 在此之前已对空 Brokers 做了显式校验并提前返回。
