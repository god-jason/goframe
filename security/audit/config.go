package audit

import "github.com/god-jason/goframe/config"

// Config 是审计配置。
type Config struct {
	Enabled       bool            `mapstructure:"enabled"`
	MaxDetailSize config.ByteSize `mapstructure:"max_detail_size"`
	SkipActions   []string        `mapstructure:"skip_actions"`
}

// DefaultConfig 返回审计默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		MaxDetailSize: 4 << 10,
	}
}
