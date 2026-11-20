package ratelimit

// Config 是限流配置。
type Config struct {
	Enabled     bool    `yaml:"enabled"`
	GlobalRate  float64 `yaml:"global_rate"`
	GlobalBurst int     `yaml:"global_burst"`
	MaxBuckets  int     `yaml:"max_buckets"`
	FailOpen    bool    `yaml:"fail_open"`
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
