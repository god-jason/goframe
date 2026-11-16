package pool

import "runtime"

// Config 是后台协程池配置。
type Config struct {
	// Workers 是并发 worker 数量；0 表示按 CPU 核数自动计算。
	Workers int `mapstructure:"workers"`
	// QueueSize 是等待队列容量；0 表示使用框架默认值。
	QueueSize int `mapstructure:"queue_size"`
}

// DefaultConfig 返回协程池默认配置。
func DefaultConfig() Config {
	return Config{
		Workers:   runtime.NumCPU() * 2,
		QueueSize: 1024,
	}
}
