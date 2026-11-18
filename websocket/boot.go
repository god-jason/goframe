package websocket

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

var (
	cfg    = config.Register("websocket", DefaultConfig())
	global = struct {
		hub       *Hub
		auth      func(c *gin.Context) (string, error)
		onMessage func(ctx context.Context, subject string, payload []byte) error
	}{}
)

// SetAuthenticator 注入握手鉴权函数，必须在 boot.Startup 前调用。
func SetAuthenticator(fn func(c *gin.Context) (string, error)) {
	global.auth = fn
}

// SetOnMessage 注入上行消息处理函数，必须在 boot.Startup 前调用。
func SetOnMessage(fn func(ctx context.Context, subject string, payload []byte) error) {
	global.onMessage = fn
}

// GetConfig 返回当前 WebSocket 配置快照。
func GetConfig() *Config { return cfg.Get() }

// Get 返回全局 WebSocket Hub；未启用或未启动时返回 nil。
func Get() *Hub {
	return global.hub
}

func init() {
	_ = boot.Register("websocket", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Startup 创建 WebSocket Hub。
func Startup(context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if !strings.HasPrefix(current.Path, "/") {
		return fmt.Errorf("websocket: path must start with /")
	}

	authenticator := global.auth
	onMessage := global.onMessage

	hub := NewHub(Options{
		Logger:         logger.Get(),
		Authenticate:   authenticator,
		OnMessage:      onMessage,
		AllowedOrigins: current.AllowedOrigins,
		MaxMessageSize: current.MaxMessageSize.Bytes(),
		ReadTimeout:    current.ReadTimeout,
		WriteTimeout:   current.WriteTimeout,
		PingInterval:   current.PingInterval,
		SendQueue:      current.SendQueue,
		InboundQueue:   current.InboundQueue,
		AllowAnonymous: current.AllowAnonymous,
		MaxTopics:      current.MaxTopics,
	})
	global.hub = hub
	return nil
}

// Shutdown 关闭 WebSocket Hub 并等待连接退出。
func Shutdown(ctx context.Context) error {
	hub := global.hub
	global.hub = nil
	if hub == nil {
		return nil
	}
	if err := hub.Close(); err != nil {
		return err
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	return hub.Wait(ctx)
}
