package ctyun

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/notify"
)

var global = struct {
	mu      sync.RWMutex
	sms     *Sms
	storage *Storage
}{}

func init() {
	_ = boot.Register("cloud-ctyun", &boot.Module{
		Startup: Startup,
		Depends: []string{"config", "logger"},
	})
}

// Startup 按配置初始化天翼云服务；未启用时直接返回。
// 短信启用时自动注册为 notify 的 sms 通道服务商。
func Startup(context.Context) error {
	current := GetConfig().normalize()
	if !current.Enabled {
		return nil
	}

	var (
		sms     *Sms
		storage *Storage
	)
	if current.SMS.Enabled {
		provider, err := NewSmsProvider(current)
		if err != nil {
			return err
		}
		sms = provider.sms
		notify.AddSMSProvider(provider)
		logger.Get().Info("ctyun sms ready",
			logger.String("endpoint", current.SMS.Endpoint),
			logger.String("sign_name", current.SMS.SignName),
		)
	}
	if current.Storage.Enabled {
		st, err := NewStorage(current)
		if err != nil {
			return err
		}
		storage = st
		logger.Get().Info("ctyun zos ready",
			logger.String("endpoint", current.Storage.Endpoint),
			logger.String("bucket", current.Storage.Bucket),
		)
	}

	global.mu.Lock()
	global.sms, global.storage = sms, storage
	global.mu.Unlock()
	return nil
}

// GetSms 返回全局短信客户端；未启用时为 nil。
func GetSms() *Sms {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.sms
}

// GetStorage 返回全局 ZOS 客户端；未启用时为 nil。
func GetStorage() *Storage {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.storage
}
