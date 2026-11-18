package logger

import (
	"context"
	"fmt"
	"strings"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var (
	cfg    = config.Register("logger", DefaultConfig())
	global = struct {
		logger Logger
		root   *ZapLogger
	}{logger: NewNop()}
)

// GetConfig 返回当前 logger 配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局 logger。boot.Startup 前返回空实现。
func Get() Logger {
	return global.logger
}

func init() {
	config.OnReload(applyReload)
	_ = boot.Register("logger", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 是 logger 的 boot 入口。
func Startup(context.Context) error {
	current := GetConfig()
	if err := validateConfig(*current); err != nil {
		return err
	}
	instance, err := New(optionsFromConfig(*current))
	if err != nil {
		return err
	}

	previous := global.root
	global.root = instance
	global.logger = instance

	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

// Shutdown 关闭全局日志文件。
func Shutdown(context.Context) error {
	instance := global.root
	global.root = nil
	global.logger = NewNop()

	if instance != nil {
		return instance.Close()
	}
	return nil
}

// applyReload 只在热更新时调整日志等级；格式/输出变更需要重启。
func applyReload() {
	level := GetConfig().Level
	current := global.logger

	if setter, ok := current.(interface{ SetLevel(string) error }); ok {
		_ = setter.SetLevel(level)
	}
}

func validateConfig(cfg Config) error {
	if _, err := parseLevel(cfg.Level); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "", "json", "console":
	default:
		return fmt.Errorf("logger: format must be json or console")
	}
	if cfg.Rotation.Enabled {
		if cfg.Rotation.MaxSize <= 0 {
			return fmt.Errorf("logger: rotation.max_size must be greater than 0")
		}
		if cfg.Rotation.MaxAge <= 0 {
			return fmt.Errorf("logger: rotation.max_age must be greater than 0")
		}
		if cfg.Rotation.MaxBackups <= 0 {
			return fmt.Errorf("logger: rotation.max_backups must be greater than 0")
		}
	}
	return nil
}
