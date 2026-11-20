package tencent

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
	voice   *Voice
	storage *Storage
}{}

func init() {
	_ = boot.Register("cloud-tencent", &boot.Module{
		Startup: Startup,
		Depends: []string{"config", "logger"},
	})
}

// Startup 按配置初始化腾讯云各服务；未启用时直接返回。
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
		logger.Get().Info("tencent sms ready",
			logger.String("region", current.Region),
			logger.String("sdk_app_id", current.SMS.SdkAppID),
		)
	}

	var voice *Voice
	if current.Voice.Enabled {
		v, err := NewVoice(current)
		if err != nil {
			return err
		}
		voice = v
		logger.Get().Info("tencent voice ready",
			logger.String("region", current.Region),
			logger.String("sdk_app_id", current.Voice.SdkAppID),
		)
	}

	var storage *Storage
	if current.Storage.Enabled {
		st, err := NewStorage(current)
		if err != nil {
			return err
		}
		storage = st
		logger.Get().Info("tencent cos ready",
			logger.String("bucket", current.Storage.Bucket),
			logger.String("endpoint", current.bucketURL()),
		)
	}

	global.mu.Lock()
	global.sms, global.voice, global.storage = sms, voice, storage
	global.mu.Unlock()
	return nil
}

// GetSms 返回全局短信客户端；未启用时为 nil。
func GetSms() *Sms {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.sms
}

// GetVoice 返回全局语音客户端；未启用时为 nil。
func GetVoice() *Voice {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.voice
}

// GetStorage 返回全局 COS 客户端；未启用时为 nil。
func GetStorage() *Storage {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.storage
}
