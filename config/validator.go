package config

import (
	"fmt"
	"net"
	"strings"
)

// Validate 校验配置的合法性与安全性。
// 初始化阶段一旦校验失败必须终止启动，不允许带着不完整配置进入运行状态。
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.App.Name) == "" {
		add("app.name must not be empty")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		add("server.port must be between 1 and 65535")
	}
	switch c.Server.Mode {
	case "debug", "release", "test":
	default:
		add("server.mode must be one of debug, release, test")
	}
	if c.Server.ReadTimeout < 0 || c.Server.WriteTimeout < 0 || c.Server.IdleTimeout < 0 {
		add("server timeouts must not be negative")
	}
	if c.Server.ShutdownTimeout <= 0 {
		add("server.shutdown_timeout must be greater than 0")
	}
	if !strings.HasPrefix(c.Server.APIPrefix, "/") {
		add("server.api_prefix must start with /")
	}
	for _, proxy := range c.Server.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				add("server.trusted_proxies contains invalid address %q", proxy)
			}
		}
	}
	if c.IsProduction() && c.Server.Mode == "debug" {
		add("server.mode must not be debug in production")
	}

	switch c.Database.Driver {
	case "":
	case "mysql", "postgres":
		if strings.TrimSpace(c.Database.DSN) == "" {
			add("database.dsn is required when database.driver is set")
		}
	default:
		add("database.driver must be empty, mysql or postgres")
	}
	switch c.Database.LogLevel {
	case "", "silent", "error", "warn", "info":
	default:
		add("database.log_level must be one of silent, error, warn, info")
	}
	if c.Redis.Enabled && strings.TrimSpace(c.Redis.Addr) == "" {
		add("redis.addr is required when redis.enabled is true")
	}

	switch c.Logger.Level {
	case "debug", "info", "warn", "error":
	default:
		add("logger.level must be one of debug, info, warn, error")
	}
	switch c.Logger.Format {
	case "json", "console":
	default:
		add("logger.format must be json or console")
	}

	if c.Security.Auth.Enabled {
		if c.Security.Auth.AccessTokenTTL <= 0 {
			add("security.auth.access_token_ttl must be greater than 0")
		}
		if c.Security.Auth.RefreshTokenTTL <= c.Security.Auth.AccessTokenTTL {
			add("security.auth.refresh_token_ttl must be greater than access_token_ttl")
		}
		if c.IsProduction() {
			if len(c.Security.Auth.Secret) < 32 {
				add("security.auth.secret must contain at least 32 characters in production")
			}
			if strings.EqualFold(c.Security.Auth.Secret, "changeme") {
				add("security.auth.secret must not use the default value in production")
			}
		}
		if c.Security.Password.Algorithm != "" && c.Security.Password.Algorithm != "argon2id" {
			add("security.password.algorithm must be argon2id")
		}
		if c.Security.Password.Memory < 8*1024 {
			add("security.password.memory must be at least 8192 KiB")
		}
		if c.Security.Password.Iterations < 1 {
			add("security.password.iterations must be at least 1")
		}
		if c.Security.Password.Parallelism < 1 {
			add("security.password.parallelism must be at least 1")
		}
	}

	if c.Security.CORS.Enabled {
		if c.Security.CORS.AllowCredentials {
			for _, origin := range c.Security.CORS.AllowedOrigins {
				if origin == "*" {
					add("security.cors.allowed_origins must not contain * when allow_credentials is true")
				}
			}
		}
	}
	if c.Security.Request.MaxBodySize <= 0 {
		add("security.request.max_body_size must be greater than 0")
	}
	if c.Security.Request.MaxQueryLength < 0 {
		add("security.request.max_query_length must not be negative")
	}
	if c.Security.RateLimit.Enabled {
		if c.Security.RateLimit.GlobalRate <= 0 {
			add("security.rate_limit.global_rate must be greater than 0")
		}
		if c.Security.RateLimit.GlobalBurst <= 0 {
			add("security.rate_limit.global_burst must be greater than 0")
		}
	}
	if c.Security.CSRF.Enabled {
		if c.Security.CSRF.CookieName == "" {
			add("security.csrf.cookie_name must not be empty")
		}
		if c.Security.CSRF.HeaderName == "" {
			add("security.csrf.header_name must not be empty")
		}
		if c.Security.CSRF.TTL <= 0 {
			add("security.csrf.ttl must be greater than 0")
		}
	}
	if c.Security.Upload.MaxSize <= 0 {
		add("security.upload.max_size must be greater than 0")
	}

	switch c.TLS.Mode {
	case "none":
	case "file":
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			add("tls.cert_file and tls.key_file are required when tls.mode is file")
		}
	case "acme":
		if len(c.TLS.ACME.Domains) == 0 {
			add("tls.acme.domains must not be empty when tls.mode is acme")
		}
	default:
		add("tls.mode must be one of none, file, acme")
	}

	switch c.Storage.Driver {
	case "local":
		if strings.TrimSpace(c.Storage.Path) == "" {
			add("storage.path is required when storage.driver is local")
		}
	case "s3", "minio":
		if c.Storage.Endpoint == "" || c.Storage.Bucket == "" {
			add("storage.endpoint and storage.bucket are required when storage.driver is s3 or minio")
		}
	default:
		add("storage.driver must be one of local, s3, minio")
	}

	if c.SPA.Enabled {
		if c.SPA.Mode != "embed" && c.SPA.Mode != "directory" {
			add("spa.mode must be embed or directory")
		}
		if strings.TrimSpace(c.SPA.Path) == "" {
			add("spa.path is required when spa.enabled is true")
		}
	}

	if c.Scheduler.Enabled && c.Scheduler.JobTimeout <= 0 {
		add("scheduler.job_timeout must be greater than 0")
	}

	if len(problems) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}
