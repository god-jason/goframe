package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// PasswordConfig 是密码哈希配置。
type PasswordConfig struct {
	Algorithm   string `mapstructure:"algorithm"`
	Memory      uint32 `mapstructure:"memory"`
	Iterations  uint32 `mapstructure:"iterations"`
	Parallelism uint8  `mapstructure:"parallelism"`
}

// Config 是认证配置。
type Config struct {
	Enabled         bool           `mapstructure:"enabled"`
	Issuer          string         `mapstructure:"issuer"`
	Audience        string         `mapstructure:"audience"`
	Secret          string         `mapstructure:"secret"`
	AccessTokenTTL  time.Duration  `mapstructure:"access_token_ttl"`
	RefreshTokenTTL time.Duration  `mapstructure:"refresh_token_ttl"`
	Leeway          time.Duration  `mapstructure:"leeway"`
	Password        PasswordConfig `mapstructure:"password"`
}

// DefaultConfig 返回认证默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		Issuer:          "goframe",
		Audience:        "goframe-api",
		AccessTokenTTL:  30 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		Leeway:          30 * time.Second,
		Password: PasswordConfig{
			Algorithm:   "argon2id",
			Memory:      64 * 1024,
			Iterations:  3,
			Parallelism: 1,
		},
	}
}

// Validate 校验认证配置。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.AccessTokenTTL <= 0 {
		return fmt.Errorf("auth: access_token_ttl must be greater than 0")
	}
	if c.RefreshTokenTTL <= c.AccessTokenTTL {
		return fmt.Errorf("auth: refresh_token_ttl must be greater than access_token_ttl")
	}
	if c.Password.Algorithm != "" && c.Password.Algorithm != "argon2id" {
		return fmt.Errorf("auth: password algorithm must be argon2id")
	}
	if c.Password.Memory < 8*1024 {
		return fmt.Errorf("auth: password memory must be at least 8192 KiB")
	}
	if c.Password.Iterations < 1 {
		return fmt.Errorf("auth: password iterations must be at least 1")
	}
	if c.Password.Parallelism < 1 {
		return fmt.Errorf("auth: password parallelism must be at least 1")
	}
	if strings.EqualFold(config.App().Environment, "production") {
		if len(c.Secret) < 32 {
			return fmt.Errorf("auth: secret must contain at least 32 characters in production")
		}
		if strings.EqualFold(c.Secret, "changeme") {
			return fmt.Errorf("auth: secret must not use the default value in production")
		}
	}
	return nil
}
