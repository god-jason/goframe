package websocket

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是 WebSocket Hub 配置。
type Config struct {
	Enabled        bool            `mapstructure:"enabled"`
	Path           string          `mapstructure:"path"`
	AllowedOrigins []string        `mapstructure:"allowed_origins"`
	MaxMessageSize config.ByteSize `mapstructure:"max_message_size"`
	ReadTimeout    time.Duration   `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration   `mapstructure:"write_timeout"`
	PingInterval   time.Duration   `mapstructure:"ping_interval"`
	SendQueue      int             `mapstructure:"send_queue"`
	InboundQueue   int             `mapstructure:"inbound_queue"`
	AllowAnonymous bool            `mapstructure:"allow_anonymous"`
}

// DefaultConfig 返回 WebSocket 默认配置。
func DefaultConfig() Config {
	return Config{
		Path:           "/ws",
		MaxMessageSize: 1 << 20,
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   10 * time.Second,
		PingInterval:   30 * time.Second,
		SendQueue:      256,
		InboundQueue:   64,
	}
}
