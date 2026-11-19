package mqtt

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/tlscfg"
)

var (
	cfg    = config.Register("mqtt", DefaultConfig())
	global = struct {
		client *Client
	}{}
)

// GetConfig 返回当前 MQTT 客户端配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局 MQTT 客户端；未启用或未启动时返回 nil。
func Get() *Client {
	return global.client
}

func init() {
	_ = boot.Register("mqtt", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "broker"},
	})
}

// Startup 创建并连接 MQTT 客户端。
func Startup(ctx context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if len(current.Brokers) == 0 {
		return fmt.Errorf("mqtt: brokers must not be empty when enabled")
	}

	options := Options{
		Logger:            logger.Get(),
		Brokers:           current.Brokers,
		ClientID:          current.ClientID,
		Username:          current.Username,
		Password:          current.Password,
		KeepAlive:         current.KeepAlive,
		ConnectTimeout:    current.ConnectTimeout,
		ReconnectInterval: current.ReconnectInterval,
		CleanSession:      current.CleanSession,
		AutoReconnect:     current.AutoReconnect,
	}
	if current.TLS.Enabled {
		tlsConfig, err := tlscfg.Client(
			current.TLS.CAFile,
			current.TLS.CertFile,
			current.TLS.KeyFile,
			current.TLS.ServerName,
		)
		if err != nil {
			return err
		}
		options.TLSConfig = tlsConfig
	}

	client, err := New(options)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	global.client = client
	return nil
}

// Shutdown 断开 MQTT 客户端。
func Shutdown(ctx context.Context) error {
	client := global.client
	global.client = nil
	if client != nil {
		return client.Stop(ctx)
	}
	return nil
}
