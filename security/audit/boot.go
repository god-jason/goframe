package audit

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("security.audit", DefaultConfig())
	global = struct {
		auditor Auditor
	}{auditor: NopAuditor{}}
)

// GetConfig 返回当前审计配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局审计器。未启用时返回空实现。
func Get() Auditor {
	return global.auditor
}

func init() {
	_ = boot.Register("audit", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建审计器。
func Startup(context.Context) error {
	current := GetConfig()
	auditor := Auditor(NopAuditor{})
	if current.Enabled {
		auditor = NewLogAuditor(logger.Get(), current.MaxDetailSize.Bytes())
	}
	global.auditor = auditor
	return nil
}

// Shutdown 释放审计器。
func Shutdown(context.Context) error {
	global.auditor = NopAuditor{}
	return nil
}
