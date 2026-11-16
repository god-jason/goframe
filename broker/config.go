package broker

import "time"

// TLSConfig 是 Broker 监听 TLS 配置。
type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// Config 是嵌入式 MQTT Broker 配置。
type Config struct {
	Enabled            bool          `mapstructure:"enabled"`
	Addr               string        `mapstructure:"addr"`
	AllowAnonymous     bool          `mapstructure:"allow_anonymous"`
	MaxPacketSize      int64         `mapstructure:"max_packet_size"`
	MaxClients         int           `mapstructure:"max_clients"`
	MaxSubscriptions   int           `mapstructure:"max_subscriptions"`
	MaxQueuedMessages  int           `mapstructure:"max_queued_messages"`
	SessionExpiry      time.Duration `mapstructure:"session_expiry"`
	WriteTimeout       time.Duration `mapstructure:"write_timeout"`
	ConnectTimeout     time.Duration `mapstructure:"connect_timeout"`
	RetransmitInterval time.Duration `mapstructure:"retransmit_interval"`
	SendQueue          int           `mapstructure:"send_queue"`
	TLS                TLSConfig     `mapstructure:"tls"`
}

// DefaultConfig 返回 Broker 默认配置。
func DefaultConfig() Config {
	return Config{
		Addr:               ":1883",
		MaxPacketSize:      1 << 20,
		MaxClients:         10000,
		MaxSubscriptions:   100,
		MaxQueuedMessages:  100,
		SessionExpiry:      24 * time.Hour,
		WriteTimeout:       10 * time.Second,
		ConnectTimeout:     10 * time.Second,
		RetransmitInterval: 10 * time.Second,
		SendQueue:          256,
	}
}
