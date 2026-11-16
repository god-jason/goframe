package csrf

import "time"

// Config 是 CSRF 配置。
type Config struct {
	Enabled    bool          `mapstructure:"enabled"`
	CookieName string        `mapstructure:"cookie_name"`
	HeaderName string        `mapstructure:"header_name"`
	TTL        time.Duration `mapstructure:"ttl"`
	Secure     bool          `mapstructure:"secure"`
	Secret     string        `mapstructure:"secret"`
}

// DefaultConfig 返回 CSRF 默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        12 * time.Hour,
	}
}
