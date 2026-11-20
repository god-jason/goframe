// Package sysinfo 提供 CPU、内存、磁盘占用的实时采集与 HTTP 接口。
//
// 数据在请求时即时采集，不落盘、不保留历史，适合运维面板与临时排障。
package sysinfo

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是系统信息接口配置。
type Config struct {
	// Enabled 控制是否注册系统信息接口。
	Enabled bool `yaml:"enabled"`
	// Path 是接口前缀，默认 /system。
	Path string `yaml:"path"`
	// SampleInterval 是 CPU 使用率的采样窗口。
	SampleInterval time.Duration `yaml:"sample_interval"`
	// Timeout 是单次采集超时。
	Timeout time.Duration `yaml:"timeout"`
}

// DefaultConfig 返回系统信息默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		Path:           "/system",
		SampleInterval: 300 * time.Millisecond,
		Timeout:        3 * time.Second,
	}
}

var cfg = config.Register("sysinfo", DefaultConfig())

// GetConfig 返回当前配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置，保证采集不会永久阻塞。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if c.Path == "" {
		c.Path = defaults.Path
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = defaults.SampleInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = defaults.Timeout
	}
	if c.Timeout <= c.SampleInterval {
		c.Timeout = c.SampleInterval + time.Second
	}
	return c
}
