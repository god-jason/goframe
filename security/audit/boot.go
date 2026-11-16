package audit

import (
	"context"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("security.audit", DefaultConfig())
	global = struct {
		sync.RWMutex
		auditor Auditor
	}{auditor: NopAuditor{}}
)

// Current 返回当前审计配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局审计器。未启用时返回空实现。
func Get() Auditor {
	global.RLock()
	defer global.RUnlock()
	return global.auditor
}

func init() {
	_ = boot.Register("audit", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建审计器。
func Startup(context.Context) error {
	current := Current()
	auditor := Auditor(NopAuditor{})
	if current.Enabled {
		auditor = NewLogAuditor(logger.Get(), current.MaxDetailSize.Bytes())
	}
	global.Lock()
	global.auditor = auditor
	global.Unlock()
	return nil
}

// Shutdown 释放审计器。
func Shutdown(context.Context) error {
	global.Lock()
	global.auditor = NopAuditor{}
	global.Unlock()
	return nil
}
