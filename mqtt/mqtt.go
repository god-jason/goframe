// Package mqtt 提供带生命周期的 MQTT 客户端封装。
//
// 职责：
//   - 统一 Broker、认证、TLS、KeepAlive、重连等配置；
//   - 提供 Publish / PublishJSON / Subscribe / Unsubscribe；
//   - 消息回调 panic 隔离，避免单个业务处理器拖垮连接；
//   - 将连接状态与错误写入框架日志，供健康检查与排障使用。
//
// 框架不内置 MQTT Broker；需要内嵌 Broker 时应单独引入（例如 mochi-mqtt）。
package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/god-jason/goframe/id"
	"github.com/god-jason/goframe/logger"
)

var (
	// ErrNoBrokers 表示未配置 Broker 地址。
	ErrNoBrokers = errors.New("mqtt: brokers must not be empty")
	// ErrNotConnected 表示客户端尚未连接。
	ErrNotConnected = errors.New("mqtt: client is not connected")
	// ErrInvalidTopic 表示 Topic 非法。
	ErrInvalidTopic = errors.New("mqtt: invalid topic")
)

// Options 是 MQTT 客户端配置。
type Options struct {
	Logger            logger.Logger
	Brokers           []string
	ClientID          string
	Username          string
	Password          string
	KeepAlive         time.Duration
	ConnectTimeout    time.Duration
	ReconnectInterval time.Duration
	CleanSession      bool
	AutoReconnect     bool
	TLSConfig         *tls.Config
}

// Stats 是客户端状态快照。
type Stats struct {
	ClientID  string   `json:"client_id"`
	Brokers   []string `json:"brokers"`
	Connected bool     `json:"connected"`
}

// Client 是 MQTT 客户端。
type Client struct {
	client    paho.Client
	log       logger.Logger
	clientID  string
	brokers   []string
	connected atomic.Bool
	stopOnce  sync.Once
}

// New 创建 MQTT 客户端。New 不发起连接，需显式调用 Start。
func New(opts Options) (*Client, error) {
	if len(opts.Brokers) == 0 {
		return nil, ErrNoBrokers
	}
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 30 * time.Second
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 10 * time.Second
	}
	if opts.ReconnectInterval <= 0 {
		opts.ReconnectInterval = 5 * time.Second
	}
	if opts.ClientID == "" {
		opts.ClientID = "goframe-" + id.New()[:8]
	}

	pahoOpts := paho.NewClientOptions()
	for _, broker := range opts.Brokers {
		pahoOpts.AddBroker(normalizeBroker(broker))
	}
	pahoOpts.SetClientID(opts.ClientID)
	pahoOpts.SetKeepAlive(opts.KeepAlive)
	pahoOpts.SetConnectTimeout(opts.ConnectTimeout)
	pahoOpts.SetCleanSession(opts.CleanSession)
	pahoOpts.SetAutoReconnect(opts.AutoReconnect)
	pahoOpts.SetMaxReconnectInterval(opts.ReconnectInterval)
	if opts.Username != "" {
		pahoOpts.SetUsername(opts.Username)
	}
	if opts.Password != "" {
		pahoOpts.SetPassword(opts.Password)
	}
	if opts.TLSConfig != nil {
		pahoOpts.SetTLSConfig(opts.TLSConfig)
	}

	c := &Client{
		log:      opts.Logger,
		clientID: opts.ClientID,
		brokers:  append([]string(nil), opts.Brokers...),
	}
	pahoOpts.SetOnConnectHandler(func(client paho.Client) {
		c.connected.Store(true)
		c.log.Info("mqtt connected",
			logger.String("client_id", opts.ClientID),
			logger.Strings("brokers", opts.Brokers),
		)
	})
	pahoOpts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		c.connected.Store(false)
		c.log.Warn("mqtt connection lost",
			logger.String("client_id", opts.ClientID),
			logger.Error(err),
		)
	})
	pahoOpts.SetReconnectingHandler(func(paho.Client, *paho.ClientOptions) {
		c.log.Info("mqtt reconnecting", logger.String("client_id", opts.ClientID))
	})
	c.client = paho.NewClient(pahoOpts)
	return c, nil
}

// Start 建立连接。
func (c *Client) Start(ctx context.Context) error {
	if c.client.IsConnected() {
		c.connected.Store(true)
		return nil
	}
	c.log.Debug("mqtt connecting", logger.String("client_id", c.clientID))
	token := c.client.Connect()
	if err := waitToken(ctx, token); err != nil {
		return fmt.Errorf("mqtt: connect: %w", err)
	}
	c.connected.Store(true)
	return nil
}

// Stop 断开连接。paho 的 Disconnect 带固定等待时间，这里只做一次幂等关闭。
func (c *Client) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.stopOnce.Do(func() {
		c.connected.Store(false)
		// 无论当前是否已连接都调用 Disconnect，确保自动重连循环也被终止。
		c.client.Disconnect(250)
		c.log.Info("mqtt disconnected", logger.String("client_id", c.clientID))
	})
	return nil
}

// IsConnected 返回是否已连接。
func (c *Client) IsConnected() bool { return c.connected.Load() && c.client.IsConnected() }

// Stats 返回状态快照。
func (c *Client) Stats() Stats {
	return Stats{ClientID: c.clientID, Brokers: append([]string(nil), c.brokers...), Connected: c.IsConnected()}
}

// Publish 发布消息。topic 不允许包含通配符。
func (c *Client) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	if err := validatePublishTopic(topic); err != nil {
		return err
	}
	if !c.IsConnected() {
		return ErrNotConnected
	}
	token := c.client.Publish(topic, qos, retained, payload)
	return waitToken(ctx, token)
}

// PublishJSON 将 value 序列化为 JSON 后发布。
func (c *Client) PublishJSON(ctx context.Context, topic string, qos byte, retained bool, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("mqtt: marshal payload: %w", err)
	}
	return c.Publish(ctx, topic, qos, retained, payload)
}

// Subscribe 订阅 Topic。handler 在 paho 的接收 goroutine 中执行，
// 内部会捕获 panic 并记录堆栈，不影响连接。
func (c *Client) Subscribe(ctx context.Context, topic string, qos byte, handler func(topic string, payload []byte)) error {
	if handler == nil {
		return fmt.Errorf("mqtt: handler is nil")
	}
	if err := validateSubscribeTopic(topic); err != nil {
		return err
	}
	if !c.IsConnected() {
		return ErrNotConnected
	}
	token := c.client.Subscribe(topic, qos, func(_ paho.Client, msg paho.Message) {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.log.Error("mqtt message handler panic",
					logger.String("topic", msg.Topic()),
					logger.Any("panic", recovered),
					logger.String("stack", string(debug.Stack())),
				)
			}
		}()
		payload := append([]byte(nil), msg.Payload()...)
		handler(msg.Topic(), payload)
	})
	return waitToken(ctx, token)
}

// Unsubscribe 取消订阅。
func (c *Client) Unsubscribe(ctx context.Context, topics ...string) error {
	if len(topics) == 0 {
		return nil
	}
	for _, topic := range topics {
		if err := validateSubscribeTopic(topic); err != nil {
			return err
		}
	}
	if !c.IsConnected() {
		return ErrNotConnected
	}
	return waitToken(ctx, c.client.Unsubscribe(topics...))
}

// waitToken 将 paho Token 等待与 context 取消结合。
// 使用带超时的轮询而不是额外 goroutine，ctx 取消后不会遗留等待协程。
func waitToken(ctx context.Context, token paho.Token) error {
	if token == nil {
		return nil
	}
	for {
		if token.WaitTimeout(100 * time.Millisecond) {
			return token.Error()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func normalizeBroker(broker string) string {
	broker = strings.TrimSpace(broker)
	if broker == "" {
		return broker
	}
	if strings.Contains(broker, "://") {
		return broker
	}
	return "tcp://" + broker
}

func validatePublishTopic(topic string) error {
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidTopic)
	}
	if len(topic) > 65535 {
		return fmt.Errorf("%w: too long", ErrInvalidTopic)
	}
	if strings.ContainsAny(topic, "+#\x00") {
		return fmt.Errorf("%w: publish topic must not contain wildcards", ErrInvalidTopic)
	}
	return nil
}

func validateSubscribeTopic(topic string) error {
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidTopic)
	}
	if len(topic) > 65535 {
		return fmt.Errorf("%w: too long", ErrInvalidTopic)
	}
	if strings.Contains(topic, "\x00") {
		return fmt.Errorf("%w: contains null byte", ErrInvalidTopic)
	}
	parts := strings.Split(topic, "/")
	for i, part := range parts {
		if strings.Contains(part, "#") {
			if part != "#" || i != len(parts)-1 {
				return fmt.Errorf("%w: # must be the last level", ErrInvalidTopic)
			}
		}
		if strings.Contains(part, "+") && part != "+" {
			return fmt.Errorf("%w: + must occupy a whole level", ErrInvalidTopic)
		}
	}
	return nil
}
