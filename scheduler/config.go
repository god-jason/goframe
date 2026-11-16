package scheduler

import "time"

// Config 是定时任务配置。
type Config struct {
	Enabled    bool          `mapstructure:"enabled"`
	JobTimeout time.Duration `mapstructure:"job_timeout"`
	Location   string        `mapstructure:"location"`
}

// DefaultConfig 返回定时任务默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:    true,
		JobTimeout: 5 * time.Minute,
	}
}
