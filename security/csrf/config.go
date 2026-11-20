package csrf

import "time"

// Config 是 CSRF 配置。
type Config struct {
	Enabled    bool          `yaml:"enabled"`
	CookieName string        `yaml:"cookie_name"`
	HeaderName string        `yaml:"header_name"`
	TTL        time.Duration `yaml:"ttl"`
	Secure     bool          `yaml:"secure"`
	Secret     string        `yaml:"secret"`
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
