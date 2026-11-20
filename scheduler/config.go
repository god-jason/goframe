package scheduler

import "time"

// Config 是定时任务配置。
type Config struct {
	Enabled    bool          `yaml:"enabled"`
	JobTimeout time.Duration `yaml:"job_timeout"`
	Location   string        `yaml:"location"`
}

// DefaultConfig 返回定时任务默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:    true,
		JobTimeout: 5 * time.Minute,
	}
}
