package mqtt

import "time"

// TLSConfig 是 MQTT over TLS 配置。
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CAFile     string `yaml:"ca_file"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	ServerName string `yaml:"server_name"`
}

// Config 是 MQTT 客户端配置。
type Config struct {
	Enabled           bool          `yaml:"enabled"`
	Brokers           []string      `yaml:"brokers"`
	ClientID          string        `yaml:"client_id"`
	Username          string        `yaml:"username"`
	Password          string        `yaml:"password"`
	KeepAlive         time.Duration `yaml:"keep_alive"`
	ConnectTimeout    time.Duration `yaml:"connect_timeout"`
	ReconnectInterval time.Duration `yaml:"reconnect_interval"`
	CleanSession      bool          `yaml:"clean_session"`
	AutoReconnect     bool          `yaml:"auto_reconnect"`
	TLS               TLSConfig     `yaml:"tls"`
}

// DefaultConfig 返回 MQTT 客户端默认配置。
func DefaultConfig() Config {
	return Config{
		KeepAlive:         30 * time.Second,
		ConnectTimeout:    10 * time.Second,
		ReconnectInterval: 5 * time.Second,
		AutoReconnect:     true,
	}
}
