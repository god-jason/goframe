package broker

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("broker", DefaultConfig())
	global = struct {
		sync.RWMutex
		server *Server
		auth   Authenticator
		hooks  Hooks
	}{}
)

// SetAuthenticator 注入 CONNECT 阶段鉴权器，必须在 boot.Startup 前调用。
func SetAuthenticator(auth Authenticator) {
	global.Lock()
	global.auth = auth
	global.Unlock()
}

// SetHooks 注入 Broker 生命周期与消息回调，必须在 boot.Startup 前调用。
func SetHooks(hooks Hooks) {
	global.Lock()
	global.hooks = hooks
	global.Unlock()
}

// Current 返回当前 Broker 配置快照。
func Current() *Config { return cfg.Get() }

// Get 返回全局 Broker；未启用或未启动时返回 nil。
func Get() *Server {
	global.RLock()
	defer global.RUnlock()
	return global.server
}

func init() {
	_ = boot.Register("broker", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建并启动嵌入式 MQTT Broker。
func Startup(ctx context.Context) error {
	current := Current()
	if !current.Enabled {
		return nil
	}

	global.RLock()
	authenticator := global.auth
	hooks := global.hooks
	global.RUnlock()

	options := Options{
		Addr:               current.Addr,
		MaxPacketSize:      int(current.MaxPacketSize),
		MaxClients:         current.MaxClients,
		MaxSubscriptions:   current.MaxSubscriptions,
		MaxQueuedMessages:  current.MaxQueuedMessages,
		SessionExpiry:      current.SessionExpiry,
		WriteTimeout:       current.WriteTimeout,
		ConnectTimeout:     current.ConnectTimeout,
		RetransmitInterval: current.RetransmitInterval,
		SendQueue:          current.SendQueue,
		AllowAnonymous:     current.AllowAnonymous,
		Auth:               authenticator,
		Hooks:              hooks,
		Logger:             logger.Get(),
	}
	if authenticator == nil && !current.AllowAnonymous {
		logger.Get().Warn("broker enabled without authenticator, connections will be rejected",
			logger.String("addr", current.Addr),
		)
	}

	if current.TLS.Enabled {
		certificate, err := tls.LoadX509KeyPair(current.TLS.CertFile, current.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("broker: load tls certificate: %w", err)
		}
		options.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		}
	}

	server := New(options)
	if err := server.Start(ctx); err != nil {
		return err
	}
	global.Lock()
	global.server = server
	global.Unlock()
	return nil
}

// Shutdown 关闭 Broker。
func Shutdown(ctx context.Context) error {
	global.Lock()
	server := global.server
	global.server = nil
	global.Unlock()
	if server != nil {
		return server.Shutdown(ctx)
	}
	return nil
}
