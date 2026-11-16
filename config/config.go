// Package config 定义框架的统一配置结构、默认值和配置校验。
package config

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ByteSize 表示字节数，支持在配置文件中使用 10MB、512KiB 等写法。
type ByteSize int64

// Bytes 返回字节数。
func (b ByteSize) Bytes() int64 { return int64(b) }

// String 返回可读的字节数表示。
func (b ByteSize) String() string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

var byteSizeUnits = []struct {
	suffix string
	factor float64
}{
	{"TIB", 1024 * 1024 * 1024 * 1024},
	{"TB", 1024 * 1024 * 1024 * 1024},
	{"GIB", 1024 * 1024 * 1024},
	{"GB", 1024 * 1024 * 1024},
	{"MIB", 1024 * 1024},
	{"MB", 1024 * 1024},
	{"KIB", 1024},
	{"KB", 1024},
	{"B", 1},
}

// ParseByteSize 解析带单位的字节数，例如 "10MB"、"512KiB"、"1024"。
func ParseByteSize(s string) (ByteSize, error) {
	raw := strings.ToUpper(strings.TrimSpace(s))
	if raw == "" {
		return 0, fmt.Errorf("empty byte size")
	}

	factor := float64(1)
	for _, u := range byteSizeUnits {
		if strings.HasSuffix(raw, u.suffix) {
			factor = u.factor
			raw = strings.TrimSpace(strings.TrimSuffix(raw, u.suffix))
			break
		}
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid byte size %q: must not be negative", s)
	}
	return ByteSize(value * factor), nil
}

// Config 是框架的完整配置。
type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Logger    LoggerConfig    `mapstructure:"logger"`
	Security  SecurityConfig  `mapstructure:"security"`
	TLS       TLSConfig       `mapstructure:"tls"`
	SPA       SPAConfig       `mapstructure:"spa"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Scheduler SchedulerConfig `mapstructure:"scheduler"`
	Pool      PoolConfig      `mapstructure:"pool"`
}

// AppConfig 应用基础信息。
type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
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
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DatabaseConfig 数据库配置。
type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	SlowThreshold   time.Duration `mapstructure:"slow_threshold"`
	LogLevel        string        `mapstructure:"log_level"`
}

// Enabled 数据库是否启用。
func (d DatabaseConfig) Enabled() bool { return d.Driver != "" }

// RedisConfig Redis 配置，同时作为缓存配置。
type RedisConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	Addr      string `mapstructure:"addr"`
	Username  string `mapstructure:"username"`
	Password  string `mapstructure:"password"`
	DB        int    `mapstructure:"db"`
	PoolSize  int    `mapstructure:"pool_size"`
	KeyPrefix string `mapstructure:"key_prefix"`
}

// LoggerConfig 日志配置。
type LoggerConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

// SecurityConfig 安全配置。
type SecurityConfig struct {
	Enabled    bool                  `mapstructure:"enabled"`
	Headers    HeadersConfig         `mapstructure:"headers"`
	CORS       CORSConfig            `mapstructure:"cors"`
	Request    RequestSecurityConfig `mapstructure:"request"`
	Auth       AuthConfig            `mapstructure:"auth"`
	Password   PasswordConfig        `mapstructure:"password"`
	RateLimit  RateLimitConfig       `mapstructure:"rate_limit"`
	CSRF       CSRFConfig            `mapstructure:"csrf"`
	Upload     UploadConfig          `mapstructure:"upload"`
	Audit      AuditConfig           `mapstructure:"audit"`
	Encryption EncryptionConfig      `mapstructure:"encryption"`
}

// HeadersConfig 安全响应头配置。
type HeadersConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	HSTS          bool          `mapstructure:"hsts"`
	HSTSMaxAge    time.Duration `mapstructure:"hsts_max_age"`
	CSP           string        `mapstructure:"csp"`
	CSPReportOnly bool          `mapstructure:"csp_report_only"`
}

// CORSConfig 跨域配置。
type CORSConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	AllowedOrigins   []string      `mapstructure:"allowed_origins"`
	AllowedMethods   []string      `mapstructure:"allowed_methods"`
	AllowedHeaders   []string      `mapstructure:"allowed_headers"`
	ExposedHeaders   []string      `mapstructure:"exposed_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

// RequestSecurityConfig 请求限制配置。
type RequestSecurityConfig struct {
	MaxBodySize     ByteSize `mapstructure:"max_body_size"`
	MaxQueryLength  int      `mapstructure:"max_query_length"`
	AllowedMethods  []string `mapstructure:"allowed_methods"`
	RequireJSONBody bool     `mapstructure:"require_json_body"`
}

// AuthConfig 认证配置。
type AuthConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	Issuer          string        `mapstructure:"issuer"`
	Audience        string        `mapstructure:"audience"`
	Secret          string        `mapstructure:"secret"`
	AccessTokenTTL  time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL time.Duration `mapstructure:"refresh_token_ttl"`
	Leeway          time.Duration `mapstructure:"leeway"`
}

// PasswordConfig 密码哈希配置。
type PasswordConfig struct {
	Algorithm   string `mapstructure:"algorithm"`
	Memory      uint32 `mapstructure:"memory"`
	Iterations  uint32 `mapstructure:"iterations"`
	Parallelism uint8  `mapstructure:"parallelism"`
}

// RateLimitConfig 限流配置。
type RateLimitConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	GlobalRate  float64 `mapstructure:"global_rate"`
	GlobalBurst int     `mapstructure:"global_burst"`
	FailOpen    bool    `mapstructure:"fail_open"`
}

// CSRFConfig CSRF 配置。
type CSRFConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	CookieName string        `mapstructure:"cookie_name"`
	HeaderName string        `mapstructure:"header_name"`
	TTL        time.Duration `mapstructure:"ttl"`
	Secure     bool          `mapstructure:"secure"`
	Secret     string        `mapstructure:"secret"`
}

// UploadConfig 上传配置。
type UploadConfig struct {
	MaxSize     ByteSize `mapstructure:"max_size"`
	ScanEnabled bool     `mapstructure:"scan_enabled"`
}

// AuditConfig 审计配置。
type AuditConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	MaxDetailSize ByteSize `mapstructure:"max_detail_size"`
	SkipActions   []string `mapstructure:"skip_actions"`
}

// EncryptionConfig 数据加密配置。
type EncryptionConfig struct {
	Algorithm   string `mapstructure:"algorithm"`
	KeyProvider string `mapstructure:"key_provider"`
	KeyID       string `mapstructure:"key_id"`
	Key         string `mapstructure:"key"`
}

// TLSConfig TLS 配置。
type TLSConfig struct {
	Mode     string     `mapstructure:"mode"`
	CertFile string     `mapstructure:"cert_file"`
	KeyFile  string     `mapstructure:"key_file"`
	ACME     ACMEConfig `mapstructure:"acme"`
}

// ACMEConfig ACME 自动证书配置。
type ACMEConfig struct {
	Email        string   `mapstructure:"email"`
	Domains      []string `mapstructure:"domains"`
	DirectoryURL string   `mapstructure:"directory_url"`
	Challenge    string   `mapstructure:"challenge"`
	CacheDir     string   `mapstructure:"cache_dir"`
}

// SPAConfig 前端静态资源配置。
type SPAConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Mode    string `mapstructure:"mode"`
	Path    string `mapstructure:"path"`
	Index   string `mapstructure:"index"`
}

// StorageConfig 文件存储配置。
type StorageConfig struct {
	Driver    string `mapstructure:"driver"`
	Path      string `mapstructure:"path"`
	BaseURL   string `mapstructure:"base_url"`
	Endpoint  string `mapstructure:"endpoint"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

// SchedulerConfig 定时任务配置。
type SchedulerConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	JobTimeout time.Duration `mapstructure:"job_timeout"`
	Location   string        `mapstructure:"location"`
}

// PoolConfig 后台协程池配置。
// 协程池用于承载审计落盘、通知、数据同步等可异步化的后台任务，
// 避免每个任务直接 go func 造成 goroutine 数量失控。
type PoolConfig struct {
	// Workers 是并发 worker 数量；0 表示按 CPU 核数自动计算。
	Workers int `mapstructure:"workers"`
	// QueueSize 是等待队列容量；0 表示使用框架默认值。
	QueueSize int `mapstructure:"queue_size"`
}

// Default 返回框架默认配置。默认值遵循“默认安全”原则：
// 未显式配置的跨域来源一律拒绝，数据库默认不启用。
func Default() Config {
	return Config{
		App: AppConfig{
			Name:        "goframe-app",
			Version:     "dev",
			Environment: "development",
		},
		Server: ServerConfig{
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
		},
		Database: DatabaseConfig{
			MaxOpenConns:    100,
			MaxIdleConns:    10,
			ConnMaxLifetime: 30 * time.Minute,
			SlowThreshold:   500 * time.Millisecond,
			LogLevel:        "warn",
		},
		Redis: RedisConfig{
			DB:       0,
			PoolSize: 20,
		},
		Logger: LoggerConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Security: SecurityConfig{
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
			Request: RequestSecurityConfig{
				MaxBodySize:     10 << 20,
				MaxQueryLength:  4096,
				AllowedMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
				RequireJSONBody: true,
			},
			Auth: AuthConfig{
				Enabled:         true,
				Issuer:          "goframe",
				Audience:        "goframe-api",
				AccessTokenTTL:  30 * time.Minute,
				RefreshTokenTTL: 30 * 24 * time.Hour,
				Leeway:          30 * time.Second,
			},
			Password: PasswordConfig{
				Algorithm:   "argon2id",
				Memory:      64 * 1024,
				Iterations:  3,
				Parallelism: 1,
			},
			RateLimit: RateLimitConfig{
				Enabled:     true,
				GlobalRate:  1000,
				GlobalBurst: 2000,
			},
			CSRF: CSRFConfig{
				Enabled:    true,
				CookieName: "csrf_token",
				HeaderName: "X-CSRF-Token",
				TTL:        12 * time.Hour,
			},
			Upload: UploadConfig{
				MaxSize: 50 << 20,
			},
			Audit: AuditConfig{
				Enabled:       true,
				MaxDetailSize: 4 << 10,
			},
			Encryption: EncryptionConfig{
				Algorithm:   "aes-256-gcm",
				KeyProvider: "env",
			},
		},
		TLS: TLSConfig{Mode: "none"},
		SPA: SPAConfig{Mode: "embed", Index: "index.html"},
		Storage: StorageConfig{
			Driver: "local",
			Path:   "data/storage",
		},
		Scheduler: SchedulerConfig{
			Enabled:    true,
			JobTimeout: 5 * time.Minute,
		},
		Pool: PoolConfig{
			Workers:   runtime.NumCPU() * 2,
			QueueSize: 1024,
		},
	}
}

// normalize 对配置做规范化处理，例如去除空格、补全空值。
func (c *Config) normalize() {
	c.Server.APIPrefix = strings.TrimRight(strings.TrimSpace(c.Server.APIPrefix), "/")
	if c.Server.APIPrefix == "" {
		c.Server.APIPrefix = "/api"
	}
	c.Server.APIVersion = strings.Trim(strings.TrimSpace(c.Server.APIVersion), "/")
	c.Logger.Level = strings.ToLower(strings.TrimSpace(c.Logger.Level))
	c.Logger.Format = strings.ToLower(strings.TrimSpace(c.Logger.Format))
	c.Security.Password.Algorithm = strings.ToLower(strings.TrimSpace(c.Security.Password.Algorithm))
	c.Security.Encryption.Algorithm = strings.ToLower(strings.TrimSpace(c.Security.Encryption.Algorithm))
	c.TLS.Mode = strings.ToLower(strings.TrimSpace(c.TLS.Mode))
	c.Storage.Driver = strings.ToLower(strings.TrimSpace(c.Storage.Driver))
	c.SPA.Mode = strings.ToLower(strings.TrimSpace(c.SPA.Mode))
	if c.SPA.Index == "" {
		c.SPA.Index = "index.html"
	}
}

// IsProduction 判断是否为生产环境。
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.App.Environment, "production")
}

// APIBasePath 返回 API 根路径，例如 /api/v1。
func (c *Config) APIBasePath() string {
	if c.Server.APIVersion == "" {
		return c.Server.APIPrefix
	}
	return c.Server.APIPrefix + "/" + c.Server.APIVersion
}
