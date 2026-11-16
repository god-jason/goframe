// Package websocket 提供带鉴权、Origin 校验、心跳与 panic 隔离的 WebSocket Hub。
//
// 设计目标：
//   - 默认只允许同源连接；可通过 AllowedOrigins 精确配置跨域来源；
//   - 鉴权在升级握手前完成，业务可注入 Authenticate 回调；
//   - 每个连接独立读写 goroutine，慢消费者不会阻塞广播；
//   - 消息处理 panic 被隔离，不影响 Hub 与其他连接；
//   - 支持按主题向全部/指定用户广播，并提供连接统计。
//
// 该包只负责连接与消息收发，不定义业务协议；业务通过 OnMessage 处理上行消息，
// 通过 Hub.Broadcast / Hub.Send 下发消息。
package websocket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/id"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/response"
)

// Options 是 Hub 配置。
type Options struct {
	Logger logger.Logger
	// AllowedOrigins 为空时只允许同源；"*" 表示允许任意来源（不建议生产使用）。
	AllowedOrigins []string
	MaxMessageSize int64
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	PingInterval   time.Duration
	SendQueue      int
	// InboundQueue 是上行消息处理队列长度，队列满时关闭慢连接。
	InboundQueue int
	// AllowAnonymous 为 true 时，未配置 Authenticate 也允许连接，身份为 "anonymous"。
	AllowAnonymous bool
	// Authenticate 在握手前执行，返回业务主体标识（用户 ID、设备 ID 等）。
	Authenticate func(c *gin.Context) (string, error)
	// OnMessage 处理客户端上行消息。返回错误只记录日志，不关闭连接。
	OnMessage func(ctx context.Context, subject string, payload []byte) error
}

// ClientInfo 是连接信息快照。
type ClientInfo struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"`
	ConnectedAt time.Time `json:"connected_at"`
}

// Stats 是 Hub 统计信息。
type Stats struct {
	Clients int  `json:"clients"`
	Closed  bool `json:"closed"`
}

// Hub 管理所有 WebSocket 连接。
type Hub struct {
	opts     Options
	log      logger.Logger
	upgrader websocket.Upgrader

	mu      sync.RWMutex
	clients map[*Client]struct{}
	closed  atomic.Bool
	wg      sync.WaitGroup
}

// NewHub 创建 Hub。
func NewHub(opts Options) *Hub {
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	if opts.MaxMessageSize <= 0 {
		opts.MaxMessageSize = 1 << 20
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = 60 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.SendQueue <= 0 {
		opts.SendQueue = 256
	}
	if opts.InboundQueue <= 0 {
		opts.InboundQueue = 64
	}

	h := &Hub{
		opts:    opts,
		log:     opts.Logger,
		clients: make(map[*Client]struct{}),
	}
	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return originAllowed(r, opts.AllowedOrigins)
		},
	}
	return h
}

// Handler 返回 Gin 握手处理器。
func (h *Hub) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.closed.Load() {
			response.Fail(c, apperrors.Unavailable("websocket hub is closed"))
			return
		}

		subject := "anonymous"
		if h.opts.Authenticate != nil {
			authenticated, err := h.opts.Authenticate(c)
			if err != nil {
				response.Fail(c, err)
				return
			}
			subject = authenticated
		} else if !h.opts.AllowAnonymous {
			response.Fail(c, apperrors.Unauthorized("websocket authentication required"))
			return
		}
		if subject == "" {
			subject = "anonymous"
		}

		conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			h.log.Warn("websocket upgrade failed",
				logger.String("subject", subject),
				logger.Error(err),
			)
			return
		}

		client := newClient(h, conn, subject)
		workers := 2
		if h.opts.OnMessage != nil {
			workers = 3
		}
		if !h.add(client, workers) {
			_ = conn.Close()
			return
		}
		h.log.Info("websocket connected",
			logger.String("client_id", client.id),
			logger.String("subject", subject),
			logger.String("remote", conn.RemoteAddr().String()),
		)

		go client.readPump()
		go client.writePump()
		if h.opts.OnMessage != nil {
			go client.messagePump()
		}
	}
}

// Broadcast 向所有连接广播消息，返回成功入队的连接数。
func (h *Hub) Broadcast(payload []byte) int {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	sent := 0
	for _, client := range clients {
		if client.enqueue(payload) {
			sent++
		}
	}
	return sent
}

// Send 向指定主体（用户/设备）的全部连接发送消息。
func (h *Hub) Send(subject string, payload []byte) int {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		if client.subject == subject {
			clients = append(clients, client)
		}
	}
	h.mu.RUnlock()

	sent := 0
	for _, client := range clients {
		if client.enqueue(payload) {
			sent++
		}
	}
	return sent
}

// Clients 返回当前连接信息快照。
func (h *Hub) Clients() []ClientInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	infos := make([]ClientInfo, 0, len(h.clients))
	for client := range h.clients {
		infos = append(infos, ClientInfo{ID: client.id, Subject: client.subject, ConnectedAt: client.connectedAt})
	}
	return infos
}

// Stats 返回统计信息。
func (h *Hub) Stats() Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Stats{Clients: len(h.clients), Closed: h.closed.Load()}
}

// Close 关闭 Hub 并断开所有连接。
func (h *Hub) Close() error {
	if h.closed.Swap(true) {
		return nil
	}

	h.mu.Lock()
	clients := make([]*Client, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.Unlock()

	for _, client := range clients {
		client.close()
	}
	h.log.Info("websocket hub closed", logger.Int("clients", len(clients)))
	return nil
}

// add 把连接加入 Hub，并在同一把锁内登记两个 goroutine。
// 返回 false 表示 Hub 已关闭，调用方应关闭连接。
func (h *Hub) add(client *Client, workers int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed.Load() {
		return false
	}
	h.clients[client] = struct{}{}
	h.wg.Add(workers)
	return true
}

func (h *Hub) remove(client *Client) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
	client.shutdown()
	h.wg.Done()
}

// Wait 等待所有连接 goroutine 退出，超时返回错误。
func (h *Hub) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Client 表示一个 WebSocket 连接。
type Client struct {
	hub         *Hub
	conn        *websocket.Conn
	id          string
	subject     string
	connectedAt time.Time
	send        chan []byte
	inbound     chan []byte
	closed      chan struct{}
	closeOnce   sync.Once
}

func newClient(hub *Hub, conn *websocket.Conn, subject string) *Client {
	return &Client{
		hub:         hub,
		conn:        conn,
		id:          id.New(),
		subject:     subject,
		connectedAt: time.Now(),
		send:        make(chan []byte, hub.opts.SendQueue),
		inbound:     make(chan []byte, hub.opts.InboundQueue),
		closed:      make(chan struct{}),
	}
}

func (c *Client) readPump() {
	defer c.hub.remove(c)

	c.conn.SetReadLimit(c.hub.opts.MaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.opts.ReadTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.hub.opts.ReadTimeout))
	})

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			c.hub.log.Debug("websocket read closed",
				logger.String("client_id", c.id),
				logger.String("subject", c.subject),
			)
			return
		}
		if c.hub.opts.OnMessage != nil && !c.enqueueInbound(payload) {
			return
		}
	}
}

// enqueueInbound 把上行消息放入处理队列，避免慢业务阻塞读循环。
// 队列满说明消费能力不足，关闭连接形成背压。
func (c *Client) enqueueInbound(payload []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.inbound <- payload:
		return true
	case <-c.closed:
		return false
	default:
		c.hub.log.Warn("websocket inbound queue full, closing connection",
			logger.String("client_id", c.id),
			logger.String("subject", c.subject),
		)
		c.close()
		return false
	}
}

// messagePump 串行处理上行消息，保证同一连接内消息顺序且不阻塞读循环。
func (c *Client) messagePump() {
	defer c.hub.remove(c)
	for {
		select {
		case payload := <-c.inbound:
			if err := safeOnMessage(c.hub.opts.OnMessage, c.subject, payload); err != nil {
				c.hub.log.Warn("websocket message handler failed",
					logger.String("client_id", c.id),
					logger.String("subject", c.subject),
					logger.Error(err),
				)
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(c.hub.opts.PingInterval)
	defer ticker.Stop()
	defer c.hub.remove(c)

	for {
		select {
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.opts.WriteTimeout))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.opts.WriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"))
			return
		}
	}
}

// enqueue 非阻塞入队；队列满时关闭慢连接，避免拖垮 Hub。
func (c *Client) enqueue(payload []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.send <- payload:
		return true
	case <-c.closed:
		return false
	default:
		c.hub.log.Warn("websocket slow consumer closed",
			logger.String("client_id", c.id),
			logger.String("subject", c.subject),
		)
		c.close()
		return false
	}
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

func (c *Client) shutdown() {
	c.close()
}

// originAllowed 判断握手来源。
// 未携带 Origin 的客户端（非浏览器）放行；浏览器来源默认必须同源。
func originAllowed(r *http.Request, allowed []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	for _, item := range allowed {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" {
			return true
		}
		if strings.EqualFold(strings.TrimRight(item, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	if len(allowed) > 0 {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func safeOnMessage(handler func(context.Context, string, []byte) error, subject string, payload []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("websocket: message handler panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return handler(context.Background(), subject, payload)
}
