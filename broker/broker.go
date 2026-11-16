package broker

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/god-jason/goframe/id"
	"github.com/god-jason/goframe/logger"
)

var (
	// ErrClosed 表示 Broker 已关闭。
	ErrClosed = errors.New("broker: server is closed")
	// ErrTooManyClients 表示在线连接数达到上限。
	ErrTooManyClients = errors.New("broker: too many clients")
	// ErrAnonymous 表示未开启匿名且未提供鉴权器。
	ErrAnonymous = errors.New("broker: anonymous connections are not allowed")
)

// Authenticator 在 CONNECT 阶段校验客户端身份。
type Authenticator interface {
	Authenticate(ctx context.Context, req ConnectRequest) error
}

// AuthFunc 将函数适配为 Authenticator。
type AuthFunc func(ctx context.Context, req ConnectRequest) error

// Authenticate 实现 Authenticator。
func (f AuthFunc) Authenticate(ctx context.Context, req ConnectRequest) error {
	return f(ctx, req)
}

// Hooks 是 Broker 生命周期与消息事件回调。
// 回调在连接 goroutine 中同步执行，内部会隔离 panic，不应执行长时间阻塞操作。
type Hooks struct {
	OnConnect    func(session *Session)
	OnDisconnect func(session *Session, err error)
	OnPublish    func(session *Session, message Message)
	OnSubscribe  func(session *Session, subscriptions []Subscription)
}

// Options 是 Broker 配置。
type Options struct {
	Addr              string
	MaxPacketSize     int
	MaxClients        int
	MaxSubscriptions  int
	MaxQueuedMessages int
	SessionExpiry     time.Duration
	WriteTimeout      time.Duration
	ConnectTimeout    time.Duration
	// RetransmitInterval 是 QoS 1/2 未确认消息的重发间隔。
	RetransmitInterval time.Duration
	SendQueue          int
	AllowAnonymous     bool
	Auth               Authenticator
	Hooks              Hooks
	Logger             logger.Logger
	TLSConfig          *tls.Config
}

// DefaultOptions 返回默认配置。
func DefaultOptions() Options {
	return Options{
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

// Stats 是 Broker 运行统计。
type Stats struct {
	Clients         int    `json:"clients"`
	Online          int64  `json:"online"`
	Subscriptions   int    `json:"subscriptions"`
	Retained        int    `json:"retained"`
	MessagesIn      uint64 `json:"messages_in"`
	MessagesOut     uint64 `json:"messages_out"`
	MessagesDropped uint64 `json:"messages_dropped"`
}

// SessionInfo 是会话信息快照。
type SessionInfo struct {
	ClientID      string    `json:"client_id"`
	Username      string    `json:"username"`
	RemoteAddr    string    `json:"remote_addr"`
	Online        bool      `json:"online"`
	CleanSession  bool      `json:"clean_session"`
	Subscriptions int       `json:"subscriptions"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastActive    time.Time `json:"last_active"`
}

type brokerStats struct {
	messagesIn      atomic.Uint64
	messagesOut     atomic.Uint64
	messagesDropped atomic.Uint64
}

// Server 是嵌入式 MQTT Broker。
type Server struct {
	opts  Options
	log   logger.Logger
	auth  Authenticator
	hooks Hooks

	mu       sync.RWMutex
	sessions map[string]*Session
	retained map[string]Message
	// filters 是订阅索引：filter -> session -> qos。
	// 路由时只遍历实际存在的过滤器，避免每条消息扫描全部会话。
	filters  map[string]map[*Session]byte
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	started  atomic.Bool
	stopped  atomic.Bool
	online   atomic.Int64
	nextSID  atomic.Uint64
	stats    brokerStats
	wg       sync.WaitGroup
}

// New 创建 Broker。New 不监听端口，需显式调用 Start。
func New(opts Options) *Server {
	defaults := DefaultOptions()
	if opts.Addr == "" {
		opts.Addr = defaults.Addr
	}
	if opts.MaxPacketSize <= 0 {
		opts.MaxPacketSize = defaults.MaxPacketSize
	}
	if opts.MaxClients <= 0 {
		opts.MaxClients = defaults.MaxClients
	}
	if opts.MaxSubscriptions <= 0 {
		opts.MaxSubscriptions = defaults.MaxSubscriptions
	}
	if opts.MaxQueuedMessages <= 0 {
		opts.MaxQueuedMessages = defaults.MaxQueuedMessages
	}
	if opts.SessionExpiry <= 0 {
		opts.SessionExpiry = defaults.SessionExpiry
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = defaults.WriteTimeout
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = defaults.ConnectTimeout
	}
	if opts.RetransmitInterval <= 0 {
		opts.RetransmitInterval = defaults.RetransmitInterval
	}
	if opts.SendQueue <= 0 {
		opts.SendQueue = defaults.SendQueue
	}
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	return &Server{
		opts:     opts,
		log:      opts.Logger,
		auth:     opts.Auth,
		hooks:    opts.Hooks,
		sessions: make(map[string]*Session),
		retained: make(map[string]Message),
		filters:  make(map[string]map[*Session]byte),
	}
}

// Addr 返回监听地址；未启动时返回配置地址。
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.opts.Addr
}

// Start 启动监听。
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started.Load() {
		s.mu.Unlock()
		return nil
	}
	if s.stopped.Load() {
		s.mu.Unlock()
		return ErrClosed
	}

	listener, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("broker: listen %s: %w", s.opts.Addr, err)
	}
	if s.opts.TLSConfig != nil {
		listener = tls.NewListener(listener, s.opts.TLSConfig)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.listener = listener
	s.ctx = runCtx
	s.cancel = cancel
	s.started.Store(true)
	s.mu.Unlock()

	s.wg.Add(2)
	go s.acceptLoop(listener)
	go s.maintenanceLoop(runCtx)
	s.log.Info("mqtt broker started",
		logger.String("addr", listener.Addr().String()),
		logger.Bool("tls", s.opts.TLSConfig != nil),
	)
	return nil
}

// Shutdown 停止 Broker 并断开所有连接。
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started.Load() {
		s.mu.Unlock()
		return nil
	}
	s.stopped.Store(true)
	s.started.Store(false)
	listener := s.listener
	cancel := s.cancel
	sessions := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	if cancel != nil {
		cancel()
	}
	for _, session := range sessions {
		session.suppressWill.Store(true)
		session.close()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn("mqtt broker shutdown timeout")
		return ctx.Err()
	}

	s.mu.Lock()
	s.sessions = make(map[string]*Session)
	s.retained = make(map[string]Message)
	s.filters = make(map[string]map[*Session]byte)
	s.listener = nil
	s.mu.Unlock()
	s.online.Store(0)
	s.log.Info("mqtt broker stopped")
	return nil
}

// Stats 返回运行统计。
func (s *Server) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	subscriptions := 0
	for _, session := range s.sessions {
		session.mu.RLock()
		subscriptions += len(session.subs)
		session.mu.RUnlock()
	}
	return Stats{
		Clients:         len(s.sessions),
		Online:          s.online.Load(),
		Subscriptions:   subscriptions,
		Retained:        len(s.retained),
		MessagesIn:      s.stats.messagesIn.Load(),
		MessagesOut:     s.stats.messagesOut.Load(),
		MessagesDropped: s.stats.messagesDropped.Load(),
	}
}

// Sessions 返回会话快照。
func (s *Server) Sessions() []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	infos := make([]SessionInfo, 0, len(s.sessions))
	for _, session := range s.sessions {
		session.mu.RLock()
		info := SessionInfo{
			ClientID:      session.clientID,
			Username:      session.username,
			RemoteAddr:    session.remoteAddr,
			Online:        session.online.Load(),
			CleanSession:  session.clean,
			Subscriptions: len(session.subs),
			ConnectedAt:   session.connectedAt,
			LastActive:    session.LastActive(),
		}
		session.mu.RUnlock()
		infos = append(infos, info)
	}
	return infos
}

// Publish 由服务端注入一条消息。
func (s *Server) Publish(topic string, payload []byte, qos byte, retain bool) error {
	if s.stopped.Load() {
		return ErrClosed
	}
	if err := validateTopicName(topic); err != nil {
		return err
	}
	if qos > 2 {
		return fmt.Errorf("broker: invalid qos %d", qos)
	}
	message := &Message{Topic: topic, Payload: append([]byte(nil), payload...), QoS: qos, Retain: retain}
	s.route(message, nil)
	return nil
}

func (s *Server) acceptLoop(listener net.Listener) {
	defer s.wg.Done()

	// 资源耗尽（如 EMFILE）等临时错误不应永久停止监听，使用退避重试。
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var backoff time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if backoff == 0 {
				backoff = 50 * time.Millisecond
			} else if backoff < time.Second {
				backoff *= 2
				if backoff > time.Second {
					backoff = time.Second
				}
			}
			s.log.Warn("broker accept failed, retrying",
				logger.Error(err),
				logger.Duration("backoff", backoff),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				continue
			}
		}
		backoff = 0
		if s.opts.MaxClients > 0 && s.online.Load() >= int64(s.opts.MaxClients) {
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("broker connection panic",
				logger.Any("panic", recovered),
				logger.String("stack", string(debug.Stack())),
			)
			_ = conn.Close()
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(s.opts.ConnectTimeout))
	reader := bufio.NewReaderSize(conn, 4096)
	first, err := readPacket(reader, s.opts.MaxPacketSize)
	if err != nil {
		_ = conn.Close()
		return
	}
	if first.typ != packetConnect {
		_ = conn.Close()
		return
	}

	req, err := parseConnect(first.payload)
	if err != nil {
		if errors.Is(err, ErrUnsupportedProtocol) {
			writeDirect(conn, buildConnack(false, 0x01))
		}
		s.log.Debug("broker reject connect", logger.Error(err), logger.String("remote", conn.RemoteAddr().String()))
		_ = conn.Close()
		return
	}
	req.RemoteAddr = conn.RemoteAddr().String()
	if req.ClientID == "" && req.CleanSession {
		req.ClientID = "auto-" + id.New()[:8]
	}
	if req.ClientID == "" {
		writeDirect(conn, buildConnack(false, 0x02))
		_ = conn.Close()
		return
	}

	if err := s.authenticate(req); err != nil {
		s.log.Warn("broker authentication failed",
			logger.String("client_id", req.ClientID),
			logger.String("remote", req.RemoteAddr),
			logger.Error(err),
		)
		writeDirect(conn, buildConnack(false, 0x05))
		_ = conn.Close()
		return
	}

	session, present, err := s.register(req, conn)
	if err != nil {
		writeDirect(conn, buildConnack(false, 0x03))
		_ = conn.Close()
		return
	}
	session.startWriter()
	// MQTT 3.1 的 CONNACK 第 1 字节为保留位，不能设置 Session Present。
	if req.ProtocolLevel == protocolLevel31 {
		present = false
	}
	session.enqueue(buildConnack(present, 0))
	for _, pending := range session.takePending() {
		message := pending.message
		session.deliver(&message, pending.qos, false)
	}
	// 持久会话重连后重发未确认的 QoS 1/2 消息。
	session.replayOutbound()
	if session.keepAlive == 0 {
		_ = conn.SetReadDeadline(time.Time{})
	}

	s.callOnConnect(session)
	s.log.Info("broker client connected",
		logger.String("client_id", session.clientID),
		logger.String("remote", session.remoteAddr),
		logger.Bool("clean", session.clean),
	)

	cleanDisconnect := false
	var closeErr error
	for {
		keepAlive := session.keepAlive
		if keepAlive > 0 {
			deadline := time.Now().Add(time.Duration(keepAlive) * 3 * time.Second / 2)
			if err := conn.SetReadDeadline(deadline); err != nil {
				closeErr = err
				break
			}
		}
		p, err := readPacket(reader, s.opts.MaxPacketSize)
		if err != nil {
			closeErr = err
			break
		}
		session.active()
		done, err := s.handlePacket(session, p)
		if err != nil {
			closeErr = err
			break
		}
		if done {
			cleanDisconnect = true
			break
		}
	}

	if !cleanDisconnect && session.will != nil && !session.suppressWill.Load() {
		s.route(cloneMessage(session.will), session)
	}
	s.detach(session, cleanDisconnect, closeErr)
}

func (s *Server) authenticate(req *ConnectRequest) (err error) {
	if s.auth == nil {
		if s.opts.AllowAnonymous {
			return nil
		}
		return ErrAnonymous
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("broker: authenticator panic: %v", recovered)
		}
	}()
	return s.auth.Authenticate(ctx, *req)
}

func (s *Server) register(req *ConnectRequest, conn net.Conn) (*Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.sessions[req.ClientID]
	sessionPresent := false
	if existing != nil {
		// 旧会话即将被替换，先清理索引，避免路由命中已失效的连接。
		s.indexRemoveSessionLocked(existing)
		if req.CleanSession {
			delete(s.sessions, req.ClientID)
			if existing.onlineCounted.CompareAndSwap(true, false) {
				s.online.Add(-1)
			}
			existing.suppressWill.Store(true)
			existing.close()
		} else {
			sessionPresent = true
			if existing.onlineCounted.CompareAndSwap(true, false) {
				s.online.Add(-1)
			}
			if existing.online.Load() {
				existing.suppressWill.Store(true)
				existing.close()
			}
		}
	}

	if s.opts.MaxClients > 0 && s.online.Load() >= int64(s.opts.MaxClients) {
		return nil, false, ErrTooManyClients
	}

	session := newSession(s.nextSID.Add(1), s, req, conn)
	if existing != nil && !req.CleanSession {
		session.copyStateFrom(existing)
	}
	s.sessions[req.ClientID] = session
	for filter, qos := range session.subs {
		s.indexSubscribeLocked(session, filter, qos)
	}
	session.onlineCounted.Store(true)
	s.online.Add(1)
	return session, sessionPresent, nil
}

func (s *Server) detach(session *Session, clean bool, closeErr error) {
	s.mu.Lock()
	if s.sessions[session.clientID] == session {
		// clean 参数表示客户端是否发送了 DISCONNECT；持久会话即使正常断开也要保留。
		if session.clean {
			delete(s.sessions, session.clientID)
			s.indexRemoveSessionLocked(session)
		} else {
			session.offlineAt = time.Now()
		}
	}
	s.mu.Unlock()

	session.close()
	if session.onlineCounted.CompareAndSwap(true, false) {
		s.online.Add(-1)
	}
	s.callOnDisconnect(session, closeErr)
	s.log.Info("broker client disconnected",
		logger.String("client_id", session.clientID),
		logger.String("remote", session.remoteAddr),
		logger.Bool("clean", clean),
		logger.Error(closeErr),
	)
}

func (s *Server) handlePacket(session *Session, p *packet) (bool, error) {
	switch p.typ {
	case packetPublish:
		message, err := parsePublish(p)
		if err != nil {
			return false, err
		}
		s.stats.messagesIn.Add(1)
		if message.QoS == 2 {
			if session.markInboundQoS2(message.PacketID) {
				s.route(message, session)
			}
			session.enqueue(buildAck(packetPubrec, message.PacketID))
			return false, nil
		}
		s.route(message, session)
		if message.QoS == 1 {
			session.enqueue(buildAck(packetPuback, message.PacketID))
		}
		return false, nil

	case packetPuback:
		packetID, err := parsePacketID(p)
		if err != nil {
			return false, err
		}
		session.handlePuback(packetID)
		return false, nil

	case packetPubrec:
		packetID, err := parsePacketID(p)
		if err != nil {
			return false, err
		}
		session.handlePubrec(packetID)
		return false, nil

	case packetPubrel:
		packetID, err := parsePacketID(p)
		if err != nil {
			return false, err
		}
		session.clearInboundQoS2(packetID)
		session.enqueue(buildAck(packetPubcomp, packetID))
		return false, nil

	case packetPubcomp:
		packetID, err := parsePacketID(p)
		if err != nil {
			return false, err
		}
		session.handlePubcomp(packetID)
		return false, nil

	case packetSubscribe:
		packetID, subscriptions, err := parseSubscribe(p)
		if err != nil {
			return false, err
		}
		codes := make([]byte, 0, len(subscriptions))
		accepted := make([]Subscription, 0, len(subscriptions))
		for _, subscription := range subscriptions {
			if session.addSubscription(subscription.Filter, subscription.QoS) {
				s.mu.Lock()
				s.indexSubscribeLocked(session, subscription.Filter, subscription.QoS)
				s.mu.Unlock()
				codes = append(codes, subscription.QoS)
				accepted = append(accepted, subscription)
			} else {
				codes = append(codes, 0x80)
			}
		}
		session.enqueue(buildSuback(packetID, codes))
		if len(accepted) > 0 {
			s.callOnSubscribe(session, accepted)
			s.deliverRetained(session, accepted)
		}
		return false, nil

	case packetUnsubscribe:
		packetID, filters, err := parseUnsubscribe(p)
		if err != nil {
			return false, err
		}
		for _, filter := range filters {
			if err := validateFilter(filter); err != nil {
				return false, err
			}
			session.removeSubscription(filter)
			s.mu.Lock()
			s.indexUnsubscribeLocked(session, filter)
			s.mu.Unlock()
		}
		session.enqueue(buildUnsuback(packetID))
		return false, nil

	case packetPingreq:
		session.enqueue(buildPingresp())
		return false, nil

	case packetDisconnect:
		return true, nil

	default:
		return false, fmt.Errorf("broker: unexpected packet type %d", p.typ)
	}
}

func (s *Server) route(message *Message, from *Session) {
	if message.Retain {
		s.updateRetained(message)
	}

	// 通过订阅索引只遍历实际存在的过滤器，避免 O(会话数×订阅数) 扫描。
	s.mu.RLock()
	matches := make(map[*Session]byte)
	for filter, subscribers := range s.filters {
		if !topicMatches(filter, message.Topic) {
			continue
		}
		for session, qos := range subscribers {
			if current, ok := matches[session]; !ok || qos > current {
				matches[session] = qos
			}
		}
	}
	s.mu.RUnlock()

	for session, qos := range matches {
		effective := message.QoS
		if qos < effective {
			effective = qos
		}
		if session.online.Load() {
			if !session.deliver(message, effective, false) && !session.clean && effective > 0 {
				session.enqueueOffline(message, effective)
			}
		} else if !session.clean && effective > 0 {
			session.enqueueOffline(message, effective)
		}
	}
	s.callOnPublish(from, *message)
}

// indexSubscribeLocked 记录订阅。调用方必须持有 s.mu。
func (s *Server) indexSubscribeLocked(session *Session, filter string, qos byte) {
	subscribers := s.filters[filter]
	if subscribers == nil {
		subscribers = make(map[*Session]byte)
		s.filters[filter] = subscribers
	}
	subscribers[session] = qos
}

// indexUnsubscribeLocked 删除单个订阅。调用方必须持有 s.mu。
func (s *Server) indexUnsubscribeLocked(session *Session, filter string) {
	subscribers := s.filters[filter]
	if subscribers == nil {
		return
	}
	delete(subscribers, session)
	if len(subscribers) == 0 {
		delete(s.filters, filter)
	}
}

// indexRemoveSessionLocked 删除会话的全部订阅索引。调用方必须持有 s.mu。
func (s *Server) indexRemoveSessionLocked(session *Session) {
	for filter, subscribers := range s.filters {
		delete(subscribers, session)
		if len(subscribers) == 0 {
			delete(s.filters, filter)
		}
	}
}

func (s *Server) updateRetained(message *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(message.Payload) == 0 {
		delete(s.retained, message.Topic)
		return
	}
	cloned := cloneMessage(message)
	cloned.Retain = true
	s.retained[message.Topic] = *cloned
}

func (s *Server) deliverRetained(session *Session, subscriptions []Subscription) {
	type retainedMatch struct {
		message Message
		qos     byte
	}
	matches := make(map[string]retainedMatch)
	s.mu.RLock()
	for topic, message := range s.retained {
		for _, subscription := range subscriptions {
			if !topicMatches(subscription.Filter, topic) {
				continue
			}
			qos := message.QoS
			if subscription.QoS < qos {
				qos = subscription.QoS
			}
			existing, ok := matches[topic]
			if !ok || qos > existing.qos {
				matches[topic] = retainedMatch{message: message, qos: qos}
			}
		}
	}
	s.mu.RUnlock()

	for _, match := range matches {
		message := match.message
		session.deliver(&message, match.qos, true)
	}
}

func (s *Server) maintenanceLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupSessions()
		}
	}
}

func (s *Server) cleanupSessions() {
	if s.opts.SessionExpiry <= 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	for clientID, session := range s.sessions {
		if session.online.Load() {
			continue
		}
		if !session.offlineAt.IsZero() && now.Sub(session.offlineAt) > s.opts.SessionExpiry {
			delete(s.sessions, clientID)
			s.indexRemoveSessionLocked(session)
		}
	}
	s.mu.Unlock()
}

func (s *Server) callOnConnect(session *Session) {
	if s.hooks.OnConnect == nil {
		return
	}
	safeHook(s.log, "OnConnect", func() { s.hooks.OnConnect(session) })
}

func (s *Server) callOnDisconnect(session *Session, err error) {
	if s.hooks.OnDisconnect == nil {
		return
	}
	safeHook(s.log, "OnDisconnect", func() { s.hooks.OnDisconnect(session, err) })
}

func (s *Server) callOnPublish(session *Session, message Message) {
	if s.hooks.OnPublish == nil {
		return
	}
	safeHook(s.log, "OnPublish", func() { s.hooks.OnPublish(session, message) })
}

func (s *Server) callOnSubscribe(session *Session, subscriptions []Subscription) {
	if s.hooks.OnSubscribe == nil {
		return
	}
	safeHook(s.log, "OnSubscribe", func() { s.hooks.OnSubscribe(session, subscriptions) })
}

func safeHook(log logger.Logger, name string, fn func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("broker hook panic",
				logger.String("hook", name),
				logger.Any("panic", recovered),
				logger.String("stack", string(debug.Stack())),
			)
		}
	}()
	fn()
}

func writeDirect(conn net.Conn, p *packet) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(encodePacket(p))
}
