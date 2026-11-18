package license

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是许可系统配置。
type Config struct {
	// Enabled 控制是否启用许可校验。
	Enabled bool `mapstructure:"enabled"`
	// File 是许可文件路径。
	File string `mapstructure:"file"`
	// PublicKey 是公钥 PEM 内容或公钥文件路径。
	PublicKey string `mapstructure:"public_key"`
	// RevocationFile 是可选的离线吊销列表路径。
	RevocationFile string `mapstructure:"revocation_file"`
	// StatusPath 是许可状态查询接口路径；为空表示不注册接口。
	StatusPath string `mapstructure:"status_path"`
	// Strict 为 true 时许可无效会导致启动失败。
	Strict bool `mapstructure:"strict"`
	// StateFile 用于记录最后校验时间并检测时钟回拨；为空表示不持久化。
	StateFile string `mapstructure:"state_file"`
	// Fingerprint 显式指定机器指纹；为空时自动计算。
	Fingerprint string `mapstructure:"fingerprint"`
	// FingerprintSources 是指纹来源优先级，如 uuid、hostid、disk、cpuid、mac。
	FingerprintSources []string `mapstructure:"fingerprint_sources"`
	// ClockTolerance 是允许的时钟误差。
	ClockTolerance time.Duration `mapstructure:"clock_tolerance"`
}

// DefaultConfig 返回许可系统默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		StatusPath:     "/license",
		Strict:         true,
		StateFile:      "data/license/state.json",
		ClockTolerance: 5 * time.Minute,
	}
}

var cfg = config.Register("license", DefaultConfig())

// GetConfig 返回当前许可配置快照。
func GetConfig() *Config { return cfg.Get() }
