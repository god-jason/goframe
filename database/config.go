package database

import (
	"fmt"
	"strings"
	"time"
)

// Config 是数据库配置。
type Config struct {
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	SlowThreshold   time.Duration `mapstructure:"slow_threshold"`
	LogLevel        string        `mapstructure:"log_level"`
}

// DefaultConfig 返回数据库默认配置。
func DefaultConfig() Config {
	return Config{
		MaxOpenConns:    100,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		SlowThreshold:   500 * time.Millisecond,
		LogLevel:        "warn",
	}
}

// Enabled 数据库是否启用。
func (c Config) Enabled() bool { return c.Driver != "" }

// Validate 校验数据库配置。
func (c Config) Validate() error {
	switch strings.ToLower(c.Driver) {
	case "":
		return nil
	case "mysql", "postgres":
		if strings.TrimSpace(c.DSN) == "" {
			return fmt.Errorf("database: dsn is required when driver is %q", c.Driver)
		}
	default:
		return fmt.Errorf("database: unsupported driver %q", c.Driver)
	}
	switch strings.ToLower(c.LogLevel) {
	case "", "silent", "error", "warn", "info":
	default:
		return fmt.Errorf("database: log_level must be one of silent, error, warn, info")
	}
	return nil
}
