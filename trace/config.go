// Package trace 提供基于 OpenTelemetry 的链路追踪能力。
package trace

import "time"

// Config 是链路追踪配置。
type Config struct {
	// Enabled 控制是否启用追踪；关闭时使用 no-op 实现。
	Enabled bool `yaml:"enabled"`
	// ServiceName 是服务名，默认使用 app.name。
	ServiceName string `yaml:"service_name"`
	// Endpoint 是 OTLP 收集器地址。
	Endpoint string `yaml:"endpoint"`
	// Protocol 是 OTLP 传输协议：http 或 grpc。
	Protocol string `yaml:"protocol"`
	// Insecure 表示使用明文连接。
	Insecure bool `yaml:"insecure"`
	// SampleRatio 是采样比例，取值 [0,1]。
	SampleRatio float64 `yaml:"sample_ratio"`
	// Headers 是导出请求附带的头部，例如鉴权信息。
	Headers map[string]string `yaml:"headers"`
	// Timeout 是单次导出超时。
	Timeout time.Duration `yaml:"timeout"`
	// BatchTimeout 是批量导出的最大等待时间。
	BatchTimeout time.Duration `yaml:"batch_timeout"`
}

// DefaultConfig 返回追踪默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:      false,
		Endpoint:     "localhost:4318",
		Protocol:     "http",
		Insecure:     true,
		SampleRatio:  1,
		Timeout:      5 * time.Second,
		BatchTimeout: 5 * time.Second,
	}
}
