package server

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是 HTTP 服务配置。
type Config struct {
	Host              string        `mapstructure:"host"`
	Port              int           `mapstructure:"port"`
	Mode              string        `mapstructure:"mode"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout   time.Duration `mapstructure:"shutdown_timeout"`
	MaxHeaderBytes    int           `mapstructure:"max_header_bytes"`
	TrustedProxies    []string      `mapstructure:"trusted_proxies"`
	APIPrefix         string        `mapstructure:"api_prefix"`
	APIVersion        string        `mapstructure:"api_version"`
}

// Addr 返回监听地址。
func (c Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// Validate 校验 HTTP 服务配置。
func (c Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("server: port must be between 1 and 65535")
	}
	switch c.Mode {
	case "debug", "release", "test":
	default:
		return fmt.Errorf("server: mode must be one of debug, release, test")
	}
	if c.ReadTimeout < 0 || c.WriteTimeout < 0 || c.IdleTimeout < 0 {
		return fmt.Errorf("server: timeouts must not be negative")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("server: shutdown_timeout must be greater than 0")
	}
	if !strings.HasPrefix(c.APIPrefix, "/") {
		return fmt.Errorf("server: api_prefix must start with /")
	}
	for _, proxy := range c.TrustedProxies {
		if net.ParseIP(proxy) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(proxy); err != nil {
			return fmt.Errorf("server: invalid trusted proxy %q", proxy)
		}
	}
	return nil
}

// DefaultConfig 返回 HTTP 服务默认配置。
func DefaultConfig() Config {
	return Config{
		Host:              "0.0.0.0",
		Port:              8080,
		Mode:              "release",
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   30 * time.Second,
		MaxHeaderBytes:    1 << 20,
		APIPrefix:         "/api",
		APIVersion:        "v1",
	}
}

// ACMEConfig 是 ACME 自动证书配置。
type ACMEConfig struct {
	Email             string        `mapstructure:"email"`
	Domains           []string      `mapstructure:"domains"`
	DirectoryURL      string        `mapstructure:"directory_url"`
	Challenge         string        `mapstructure:"challenge"`
	CacheDir          string        `mapstructure:"cache_dir"`
	HTTPChallengeAddr string        `mapstructure:"http_challenge_addr"`
	DisableHTTP       bool          `mapstructure:"disable_http_challenge"`
	RenewBefore       time.Duration `mapstructure:"renew_before"`
}

// TLSConfig 是 TLS 配置。
type TLSConfig struct {
	Mode     string     `mapstructure:"mode"`
	CertFile string     `mapstructure:"cert_file"`
	KeyFile  string     `mapstructure:"key_file"`
	ACME     ACMEConfig `mapstructure:"acme"`
}

// Validate 校验 TLS 配置。
func (c TLSConfig) Validate() error {
	switch c.Mode {
	case "none":
		return nil
	case "file":
		if c.CertFile == "" || c.KeyFile == "" {
			return fmt.Errorf("tls: cert_file and key_file are required when mode is file")
		}
	case "acme":
		if strings.TrimSpace(c.ACME.Email) == "" {
			return fmt.Errorf("tls: acme.email is required when mode is acme")
		}
		if len(c.ACME.Domains) == 0 {
			return fmt.Errorf("tls: acme.domains must not be empty when mode is acme")
		}
		switch c.ACME.Challenge {
		case "", "http-01", "tls-alpn-01":
		case "dns-01":
			return fmt.Errorf("tls: acme challenge dns-01 is not supported yet")
		default:
			return fmt.Errorf("tls: acme.challenge must be http-01 or tls-alpn-01")
		}
		if c.ACME.Challenge == "http-01" && c.ACME.DisableHTTP {
			return fmt.Errorf("tls: disable_http_challenge conflicts with challenge http-01")
		}
	default:
		return fmt.Errorf("tls: mode must be one of none, file, acme")
	}
	return nil
}

// DefaultTLSConfig 返回 TLS 默认配置。
func DefaultTLSConfig() TLSConfig {
	return TLSConfig{
		Mode: "none",
		ACME: ACMEConfig{
			HTTPChallengeAddr: ":80",
			RenewBefore:       30 * 24 * time.Hour,
		},
	}
}

var (
	cfg    = config.Register("server", DefaultConfig())
	tlsCfg = config.Register("tls", DefaultTLSConfig())
)

// Current 返回当前 HTTP 服务配置快照。
func Current() *Config { return cfg.Get() }

// TLSConfig 返回当前 TLS 配置快照。
func CurrentTLSConfig() *TLSConfig { return tlsCfg.Get() }
