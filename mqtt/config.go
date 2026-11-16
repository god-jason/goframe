package mqtt

import "time"

// TLSConfig 是 MQTT over TLS 配置。
type TLSConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	CAFile     string `mapstructure:"ca_file"`
	CertFile   string `mapstructure:"cert_file"`
	KeyFile    string `mapstructure:"key_file"`
	ServerName string `mapstructure:"server_name"`
}

// Config 是 MQTT 客户端配置。
type Config struct {
	Enabled           bool          `mapstructure:"enabled"`
	Brokers           []string      `mapstructure:"brokers"`
	ClientID          string        `mapstructure:"client_id"`
	Username          string        `mapstructure:"username"`
	Password          string        `mapstructure:"password"`
	KeepAlive         time.Duration `mapstructure:"keep_alive"`
	ConnectTimeout    time.Duration `mapstructure:"connect_timeout"`
	ReconnectInterval time.Duration `mapstructure:"reconnect_interval"`
	CleanSession      bool          `mapstructure:"clean_session"`
	AutoReconnect     bool          `mapstructure:"auto_reconnect"`
	TLS               TLSConfig     `mapstructure:"tls"`
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
