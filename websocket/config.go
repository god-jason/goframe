package websocket

import (
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是 WebSocket Hub 配置。
type Config struct {
	Enabled        bool            `yaml:"enabled"`
	Path           string          `yaml:"path"`
	AllowedOrigins []string        `yaml:"allowed_origins"`
	MaxMessageSize config.ByteSize `yaml:"max_message_size"`
	ReadTimeout    time.Duration   `yaml:"read_timeout"`
	WriteTimeout   time.Duration   `yaml:"write_timeout"`
	PingInterval   time.Duration   `yaml:"ping_interval"`
	SendQueue      int             `yaml:"send_queue"`
	InboundQueue   int             `yaml:"inbound_queue"`
	AllowAnonymous bool            `yaml:"allow_anonymous"`
	// MaxTopics 是单个连接允许订阅的主题数量上限。
	MaxTopics int `yaml:"max_topics"`
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
		MaxTopics:      100,
	}
}
