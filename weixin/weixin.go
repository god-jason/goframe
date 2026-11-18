package weixin

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/logger"
)

const defaultBaseURL = "https://api.weixin.qq.com"

// Client 是单个微信应用的鉴权客户端。
type Client struct {
	app        AppConfig
	kind       Kind
	baseURL    string
	timeout    time.Duration
	httpClient *http.Client
	cache      cache.Cache
	log        logger.Logger
	aesKey     []byte

	access *tokenStore
	ticket *tokenStore
}

// Option 是客户端构造选项。
type Option func(*Client)

// WithBaseURL 覆盖微信 API 地址，主要用于测试与代理。
func WithBaseURL(url string) Option {
	return func(c *Client) {
		if url != "" {
			c.baseURL = url
		}
	}
}

// WithCache 注入共享缓存，用于多实例共享 AccessToken。
func WithCache(value cache.Cache) Option {
	return func(c *Client) { c.cache = value }
}

// WithHTTPClient 注入自定义 HTTP 客户端。
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithLogger 注入日志实现。
func WithLogger(log logger.Logger) Option {
	return func(c *Client) {
		if log != nil {
			c.log = log
		}
	}
}

// WithTimeout 设置单次微信 API 请求超时。
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.timeout = timeout
		}
	}
}

// New 创建微信客户端。
func New(app AppConfig, options ...Option) (*Client, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	client := &Client{
		app:        app,
		kind:       NormalizeKind(app.Type),
		baseURL:    defaultBaseURL,
		timeout:    10 * time.Second,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		log:        logger.NewNop(),
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}

	if app.EncodingAESKey != "" {
		key, err := base64.StdEncoding.DecodeString(app.EncodingAESKey + "=")
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("weixin: app %q has invalid encoding_aes_key", app.Name)
		}
		client.aesKey = key
	}

	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: client.timeout}
	}
	if client.httpClient.Timeout <= 0 {
		client.httpClient.Timeout = client.timeout
	}
	// 避免测试用 httptest 时复用连接出现异常。
	if client.httpClient.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		client.httpClient.Transport = transport
	}

	client.access = newTokenStore(client.cache, "weixin:"+app.AppID+":access_token")
	client.ticket = newTokenStore(client.cache, "weixin:"+app.AppID+":jsapi_ticket")
	return client, nil
}

// Name 返回应用名称。
func (c *Client) Name() string { return c.app.Name }

// Kind 返回应用类型。
func (c *Client) Kind() Kind { return c.kind }

// AppID 返回应用 AppID。
func (c *Client) AppID() string { return c.app.AppID }

func (c *Client) requireKind(kind Kind) error {
	if c == nil {
		return fmt.Errorf("weixin: client is nil")
	}
	if c.kind != kind {
		return fmt.Errorf("weixin: app %q is %s, not %s", c.app.Name, c.kind, kind)
	}
	return nil
}

var global = struct {
	clients  map[string]*Client
	order    []string
	fallback *Client
}{}

func init() {
	_ = boot.Register("weixin", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "cache"},
	})
}

// Startup 创建配置中的微信客户端。
func Startup(context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if len(current.Apps) == 0 {
		return fmt.Errorf("weixin: apps must not be empty when enabled")
	}

	timeout := current.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sharedCache := cache.Get()

	clients := make(map[string]*Client, len(current.Apps))
	var fallback *Client
	defaults := 0
	for _, app := range current.Apps {
		if _, exists := clients[app.Name]; exists {
			return fmt.Errorf("weixin: duplicate app %q", app.Name)
		}
		client, err := New(app,
			WithCache(sharedCache),
			WithLogger(logger.Get()),
			WithTimeout(timeout),
		)
		if err != nil {
			return err
		}
		clients[app.Name] = client
		if app.Default {
			defaults++
			fallback = client
		}
		if len(clients) == 1 && fallback == nil {
			fallback = client
		}
	}
	if defaults > 1 {
		return fmt.Errorf("weixin: only one default app is allowed")
	}

	order := make([]string, 0, len(current.Apps))
	for _, app := range current.Apps {
		order = append(order, app.Name)
	}

	global.clients = clients
	global.order = order
	global.fallback = fallback

	logger.Get().Info("weixin clients ready",
		logger.Int("apps", len(clients)),
		logger.Strings("names", order),
	)
	return nil
}

// Shutdown 释放微信客户端。
func Shutdown(context.Context) error {
	global.clients = nil
	global.order = nil
	global.fallback = nil
	return nil
}

// Get 按名称返回微信客户端；名称为空时返回默认应用。
func Get(name string) *Client {
	if name == "" {
		return global.fallback
	}
	return global.clients[name]
}

// Default 返回默认微信客户端；未启用时返回 nil。
func Default() *Client { return Get("") }

// Apps 返回按配置顺序排列的应用名称。
func Apps() []string {
	return append([]string(nil), global.order...)
}
