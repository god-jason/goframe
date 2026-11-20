package outbox

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是 Outbox 投递配置。
type Config struct {
	// Enabled 控制是否自动启动后台投递。
	Enabled bool `yaml:"enabled"`
	// Interval 是轮询间隔。
	Interval time.Duration `yaml:"interval"`
	// BatchSize 是单次领取的消息数。
	BatchSize int `yaml:"batch_size"`
	// MaxAttempts 是最大投递次数。
	MaxAttempts int `yaml:"max_attempts"`
	// BaseDelay 是首次重试延迟。
	BaseDelay time.Duration `yaml:"base_delay"`
	// MaxDelay 是退避上限。
	MaxDelay time.Duration `yaml:"max_delay"`
	// LeaseTimeout 是领取租约。
	LeaseTimeout time.Duration `yaml:"lease_timeout"`
}

// DefaultConfig 返回 Outbox 默认配置。
func DefaultConfig() Config {
	defaults := DefaultOptions()
	return Config{
		Enabled:      false,
		Interval:     defaults.Interval,
		BatchSize:    defaults.BatchSize,
		MaxAttempts:  defaults.MaxAttempts,
		BaseDelay:    defaults.BaseDelay,
		MaxDelay:     defaults.MaxDelay,
		LeaseTimeout: defaults.LeaseTimeout,
	}
}

// Options 返回 Relay 选项。
func (c Config) Options() Options {
	return Options{
		Interval:     c.Interval,
		BatchSize:    c.BatchSize,
		MaxAttempts:  c.MaxAttempts,
		BaseDelay:    c.BaseDelay,
		MaxDelay:     c.MaxDelay,
		LeaseTimeout: c.LeaseTimeout,
	}
}

var cfg = config.Register("outbox", DefaultConfig())

// GetConfig 返回当前 Outbox 配置快照。
func GetConfig() *Config { return cfg.Get() }
