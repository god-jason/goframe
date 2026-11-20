package web

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// RequestConfig 是请求限制配置。
type RequestConfig struct {
	MaxBodySize     config.ByteSize `yaml:"max_body_size"`
	MaxQueryLength  int             `yaml:"max_query_length"`
	AllowedMethods  []string        `yaml:"allowed_methods"`
	RequireJSONBody bool            `yaml:"require_json_body"`
}

// Config 是 HTTP 服务配置。
type Config struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	Mode              string        `yaml:"mode"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes"`
	TrustedProxies    []string      `yaml:"trusted_proxies"`
	APIPrefix         string        `yaml:"api_prefix"`
	APIVersion        string        `yaml:"api_version"`
	// Request 是请求方法白名单与请求体等限制配置。
	Request RequestConfig `yaml:"request"`
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
		Request: RequestConfig{
			MaxBodySize:     10 << 20,
			MaxQueryLength:  4096,
			AllowedMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			RequireJSONBody: true,
		},
	}
}

// ACMEConfig 是 ACME 自动证书配置。
type ACMEConfig struct {
	Email             string        `yaml:"email"`
	Domains           []string      `yaml:"domains"`
	DirectoryURL      string        `yaml:"directory_url"`
	Challenge         string        `yaml:"challenge"`
	CacheDir          string        `yaml:"cache_dir"`
	HTTPChallengeAddr string        `yaml:"http_challenge_addr"`
	DisableHTTP       bool          `yaml:"disable_http_challenge"`
	RenewBefore       time.Duration `yaml:"renew_before"`
}

// TLSConfig 是 TLS 配置。
type TLSConfig struct {
	Mode     string     `yaml:"mode"`
	CertFile string     `yaml:"cert_file"`
	KeyFile  string     `yaml:"key_file"`
	ACME     ACMEConfig `yaml:"acme"`
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
		case "", "http-01", "tls-alpn-01", "dns-01":
		default:
			return fmt.Errorf("tls: acme.challenge must be http-01, tls-alpn-01 or dns-01")
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

// GetConfig 返回当前 HTTP 服务配置快照。
func GetConfig() *Config { return cfg.Get() }

// TLSConfig 返回当前 TLS 配置快照。
func CurrentTLSConfig() *TLSConfig { return tlsCfg.Get() }
