package payment

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/lock"
	"github.com/god-jason/goframe/logger"
)

var global = struct {
	mu      sync.RWMutex
	manager *Manager
	wechat  *wechatChannel
}{}

func init() {
	_ = boot.Register("payment", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "cache"},
	})
}

// Get 返回全局支付管理器；未启用或未启动时返回 nil。
func Get() *Manager {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.manager
}

// Startup 构建已启用渠道、解析幂等依赖并登记回调路由。
func Startup(ctx context.Context) error {
	current := GetConfig().normalize()
	if !current.Enabled {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	manager := &Manager{
		channels:      make(map[ChannelName]Channel),
		doneTTL:       current.Idempotency.DoneTTL,
		processingTTL: current.Idempotency.ProcessingTTL,
	}

	if current.Wechat.Enabled {
		wechat, err := newWechat(ctx, current.Wechat, current.Timeout)
		if err != nil {
			return err
		}
		manager.channels[ChannelWechat] = wechat
		global.mu.Lock()
		global.wechat = wechat
		global.mu.Unlock()
	}
	if current.Alipay.Enabled {
		alipay, err := newAlipay(current.Alipay, current.Timeout)
		if err != nil {
			return err
		}
		manager.channels[ChannelAlipay] = alipay
	}

	// 幂等依赖：优先复用全局 Redis 缓存做分布式去重，否则回退进程内锁。
	manager.store = cache.Get()
	if redisCache, ok := manager.store.(*cache.RedisCache); ok && redisCache != nil {
		manager.locker = lock.NewRedis(redisCache.Client(), lock.RedisOptions{Prefix: "goframe:payment:lock:"})
	} else {
		manager.locker = lock.NewMemory()
		logger.Get().Warn("payment notify idempotency falls back to in-memory lock; single-instance semantics only")
	}

	register(current)

	global.mu.Lock()
	global.manager = manager
	global.mu.Unlock()

	channels := make([]string, 0, len(manager.channels))
	for name := range manager.channels {
		channels = append(channels, string(name))
	}
	logger.Get().Info("payment channels loaded", logger.Strings("channels", channels))
	return nil
}

// Shutdown 释放支付管理器并停止微信证书后台下载。
func Shutdown(context.Context) error {
	global.mu.Lock()
	wechat := global.wechat
	global.manager = nil
	global.wechat = nil
	global.mu.Unlock()

	if wechat != nil {
		wechat.stop()
	}
	return nil
}

// loadKeyMaterial 支持内联 PEM 内容或文件路径（沿用 license 组件惯例）。
func loadKeyMaterial(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("key material is empty")
	}
	if strings.Contains(value, "BEGIN") {
		return []byte(value), nil
	}
	content, err := os.ReadFile(value)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return content, nil
}
