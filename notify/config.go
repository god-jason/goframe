package notify

import (
	"fmt"
	"strings"

	"github.com/god-jason/goframe/config"
)

// Config 是通知组件的全局配置。
type Config struct {
	// Enabled 控制是否启用通知组件。
	Enabled bool `yaml:"enabled"`
	// Channel 是发送通道：smtp / webhook。
	Channel string `yaml:"channel"`
	// Queue 是任务队列启用时投递的队列名。
	Queue string `yaml:"queue"`
	// MaxRetry 是队列任务的重试次数。
	MaxRetry int `yaml:"max_retry"`
	// SMTP 是邮件通道配置。
	SMTP SMTPConfig `yaml:"smtp"`
	// Webhook 是 Webhook 通道配置。
	Webhook WebhookConfig `yaml:"webhook"`
}

// DefaultConfig 返回通知默认配置。
func DefaultConfig() Config {
	return Config{
		Channel:  "smtp",
		Queue:    "notify",
		MaxRetry: 3,
	}
}

var cfg = config.Register("notify", DefaultConfig())

// GetConfig 返回当前通知配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(c.Channel) == "" {
		c.Channel = defaults.Channel
	}
	if strings.TrimSpace(c.Queue) == "" {
		c.Queue = defaults.Queue
	}
	if c.MaxRetry < 0 {
		c.MaxRetry = defaults.MaxRetry
	}
	return c
}

// buildSender 按通道配置构建发送器。
func (c Config) buildSender() (Sender, error) {
	switch strings.ToLower(strings.TrimSpace(c.Channel)) {
	case "smtp":
		return NewSMTP(c.SMTP)
	case "webhook":
		return NewWebhook(c.Webhook)
	default:
		return nil, fmt.Errorf("notify: unknown channel %q (want smtp or webhook)", c.Channel)
	}
}
