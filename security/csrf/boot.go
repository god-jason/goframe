package csrf

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("security.csrf", DefaultConfig())
	global = struct {
		protector *Protector
	}{}
)

// GetConfig 返回当前 CSRF 配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局 CSRF 保护器；未启用或未启动时返回 nil。
func Get() *Protector {
	return global.protector
}

func init() {
	_ = boot.Register("csrf", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 创建 CSRF 保护器。
func Startup(context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if current.CookieName == "" || current.HeaderName == "" {
		return fmt.Errorf("csrf: cookie_name and header_name are required")
	}
	if current.TTL <= 0 {
		return fmt.Errorf("csrf: ttl must be greater than 0")
	}
	protector, err := New(*current)
	if err != nil {
		return err
	}
	global.protector = protector
	return nil
}

// Shutdown 释放 CSRF 保护器。
func Shutdown(context.Context) error {
	global.protector = nil
	return nil
}
