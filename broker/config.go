package broker

import "time"

// TLSConfig 是 Broker 监听 TLS 配置。
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Config 是嵌入式 MQTT Broker 配置。
type Config struct {
	Enabled            bool          `yaml:"enabled"`
	Addr               string        `yaml:"addr"`
	AllowAnonymous     bool          `yaml:"allow_anonymous"`
	MaxPacketSize      int64         `yaml:"max_packet_size"`
	MaxClients         int           `yaml:"max_clients"`
	MaxSubscriptions   int           `yaml:"max_subscriptions"`
	MaxQueuedMessages  int           `yaml:"max_queued_messages"`
	SessionExpiry      time.Duration `yaml:"session_expiry"`
	WriteTimeout       time.Duration `yaml:"write_timeout"`
	ConnectTimeout     time.Duration `yaml:"connect_timeout"`
	RetransmitInterval time.Duration `yaml:"retransmit_interval"`
	SendQueue          int           `yaml:"send_queue"`
	TLS                TLSConfig     `yaml:"tls"`
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
