package audit

import "github.com/god-jason/goframe/config"

// Config 是审计配置。
type Config struct {
	Enabled       bool            `yaml:"enabled"`
	MaxDetailSize config.ByteSize `yaml:"max_detail_size"`
	SkipActions   []string        `yaml:"skip_actions"`
}

// DefaultConfig 返回审计默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		MaxDetailSize: 4 << 10,
	}
}
