package onenet

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/notify"
)

var global = struct {
	mu  sync.RWMutex
	sms *Sms
}{}

func init() {
	_ = boot.Register("cloud-onenet", &boot.Module{
		Startup: Startup,
		Depends: []string{"config", "logger"},
	})
}

// Startup 按配置初始化 OneNET 服务；未启用时直接返回。
// 短信启用时自动注册为 notify 的 sms 通道服务商。
func Startup(context.Context) error {
	current := GetConfig().normalize()
	if !current.Enabled {
		return nil
	}

	var sms *Sms
	if current.SMS.Enabled {
		provider, err := NewSmsProvider(current)
		if err != nil {
			return err
		}
		sms = provider.sms
		notify.AddSMSProvider(provider)
		logger.Get().Info("onenet sms ready",
			logger.String("endpoint", current.Endpoint),
			logger.String("sign_id", current.SMS.SignID),
		)
	}

	global.mu.Lock()
	global.sms = sms
	global.mu.Unlock()
	return nil
}

// GetSms 返回全局短信客户端；未启用时为 nil。
func GetSms() *Sms {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.sms
}
