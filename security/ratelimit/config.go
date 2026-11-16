package ratelimit

// Config 是限流配置。
type Config struct {
	Enabled     bool    `mapstructure:"enabled"`
	GlobalRate  float64 `mapstructure:"global_rate"`
	GlobalBurst int     `mapstructure:"global_burst"`
	MaxBuckets  int     `mapstructure:"max_buckets"`
	FailOpen    bool    `mapstructure:"fail_open"`
}

// DefaultConfig 返回限流默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:     true,
		GlobalRate:  1000,
		GlobalBurst: 2000,
		MaxBuckets:  100000,
	}
}
