package middleware

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// HeadersConfig 是安全响应头配置。
type HeadersConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	HSTS          bool          `mapstructure:"hsts"`
	HSTSMaxAge    time.Duration `mapstructure:"hsts_max_age"`
	CSP           string        `mapstructure:"csp"`
	CSPReportOnly bool          `mapstructure:"csp_report_only"`
}

// CORSConfig 是跨域配置。
type CORSConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	AllowedOrigins   []string      `mapstructure:"allowed_origins"`
	AllowedMethods   []string      `mapstructure:"allowed_methods"`
	AllowedHeaders   []string      `mapstructure:"allowed_headers"`
	ExposedHeaders   []string      `mapstructure:"exposed_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

// RequestConfig 是请求限制配置。
type RequestConfig struct {
	MaxBodySize     config.ByteSize `mapstructure:"max_body_size"`
	MaxQueryLength  int             `mapstructure:"max_query_length"`
	AllowedMethods  []string        `mapstructure:"allowed_methods"`
	RequireJSONBody bool            `mapstructure:"require_json_body"`
}

// Config 是安全中间件配置。
type Config struct {
	Enabled bool          `mapstructure:"enabled"`
	Headers HeadersConfig `mapstructure:"headers"`
	CORS    CORSConfig    `mapstructure:"cors"`
	Request RequestConfig `mapstructure:"request"`
}

// DefaultConfig 返回安全中间件默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled: true,
		Headers: HeadersConfig{
			Enabled:    true,
			HSTSMaxAge: 365 * 24 * time.Hour,
		},
		CORS: CORSConfig{
			Enabled:        true,
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type", "X-Request-ID", "X-CSRF-Token", "X-Tenant-ID"},
			MaxAge:         12 * time.Hour,
		},
		Request: RequestConfig{
			MaxBodySize:     10 << 20,
			MaxQueryLength:  4096,
			AllowedMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			RequireJSONBody: true,
		},
	}
}

var cfg = config.Register("security", DefaultConfig())

// GetConfig 返回当前安全中间件配置快照。
func GetConfig() *Config { return cfg.Get() }
