package broker

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/god-jason/goframe/logger"
)

type outboundState struct {
	qos        byte
	message    Message
	pubrelSent bool
	sentAt     time.Time
}

type pendingMessage struct {
	message Message
	qos     byte
}

// Session 表示一个 MQTT 会话。
// 清理会话在断开后删除；持久会话在断开后保留订阅和有限离线消息。
type Session struct {
	id         uint64
	broker     *Server
	clientID   string
	username   string
	remoteAddr string
	clean      bool
	keepAlive  uint16
	will       *Message
	conn       net.Conn

	mu            sync.RWMutex
	subs          map[string]byte
	pending       []pendingMessage
	outbound      map[uint16]outboundState
	inboundQoS2   map[uint16]struct{}
	nextID        uint16
	connectedAt   time.Time
	offlineAt     time.Time
	online        atomic.Bool
	onlineCounted atomic.Bool
	lastActive    atomic.Int64

	send         chan []byte
	done         chan struct{}
	closeOnce    sync.Once
	suppressWill atomic.Bool
}

func newSession(id uint64, broker *Server, req *ConnectRequest, conn net.Conn) *Session {
	s := &Session{
		id:          id,
		broker:      broker,
		clientID:    req.ClientID,
		username:    req.Username,
		remoteAddr:  req.RemoteAddr,
		clean:       req.CleanSession,
		keepAlive:   req.KeepAlive,
		will:        cloneMessage(req.Will),
		conn:        conn,
		subs:        make(map[string]byte),
		outbound:    make(map[uint16]outboundState),
		inboundQoS2: make(map[uint16]struct{}),
		connectedAt: time.Now(),
		send:        make(chan []byte, max(1, broker.opts.SendQueue)),
		done:        make(chan struct{}),
	}
	s.online.Store(true)
	s.lastActive.Store(time.Now().Unix())
	return s
}

// ID 返回内部会话 ID。
func (s *Session) ID() uint64 { return s.id }

// ClientID 返回客户端 ID。
func (s *Session) ClientID() string { return s.clientID }

// Username 返回用户名。
func (s *Session) Username() string { return s.username }

// RemoteAddr 返回客户端地址。
func (s *Session) RemoteAddr() string { return s.remoteAddr }

// CleanSession 返回是否清理会话。
func (s *Session) CleanSession() bool { return s.clean }

// KeepAlive 返回客户端声明的 KeepAlive 秒数。
func (s *Session) KeepAlive() uint16 { return s.keepAlive }

// Online 返回连接是否在线。
func (s *Session) Online() bool { return s.online.Load() }

// LastActive 返回最近活跃时间。
func (s *Session) LastActive() time.Time { return time.Unix(s.lastActive.Load(), 0) }

// Subscriptions 返回订阅快照。
func (s *Session) Subscriptions() map[string]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]byte, len(s.subs))
	for filter, qos := range s.subs {
		result[filter] = qos
	}
	return result
}

func (s *Session) startWriter() {
	go s.writeLoop()
	go s.retransmitLoop()
}

func (s *Session) writeLoop() {
	for {
		select {
		case data := <-s.send:
			if err := s.writeRaw(data); err != nil {
				s.broker.log.Debug("broker write failed",
					logger.String("client_id", s.clientID),
					logger.Error(err),
				)
				s.close()
				return
			}
		case <-s.done:
			return
		}
	}
}

// retransmitLoop 定期重发未确认的 QoS 1/2 消息。MQTT 允许在收到确认前重发，
// 使用 DUP 标记避免客户端重复处理。
func (s *Session) retransmitLoop() {
	interval := s.broker.opts.RetransmitInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			s.retransmit(interval, now)
		}
	}
}

// retransmit 重发一批超时未确认消息，单次最多 64 条，避免瞬间打满发送队列。
func (s *Session) retransmit(interval time.Duration, now time.Time) {
	const batch = 64

	s.mu.Lock()
	packets := make([]*packet, 0, batch)
	for id, state := range s.outbound {
		if len(packets) >= batch {
			break
		}
		if now.Sub(state.sentAt) < interval {
			continue
		}
		if state.qos == 2 && state.pubrelSent {
			packets = append(packets, buildAck(packetPubrel, id))
		} else {
			packets = append(packets, buildPublish(state.message.Topic, state.message.Payload, state.qos, false, true, id))
		}
		state.sentAt = now
		s.outbound[id] = state
	}
	s.mu.Unlock()

	for _, p := range packets {
		if !s.enqueue(p) {
			return
		}
	}
}

// replayOutbound 在重连后重发持久会话中未确认的消息。
// 使用阻塞入队，保证所有积压消息按序写入后再进入读循环。
func (s *Session) replayOutbound() {
	s.mu.Lock()
	packets := make([]*packet, 0, len(s.outbound))
	now := time.Now()
	for id, state := range s.outbound {
		if state.qos == 2 && state.pubrelSent {
			packets = append(packets, buildAck(packetPubrel, id))
		} else {
			packets = append(packets, buildPublish(state.message.Topic, state.message.Payload, state.qos, false, true, id))
		}
		state.sentAt = now
		s.outbound[id] = state
	}
	s.mu.Unlock()

	for _, p := range packets {
		if !s.enqueueWait(p) {
			return
		}
	}
}

// enqueueWait 阻塞入队，仅用于重连重发等可以等待写出的场景。
func (s *Session) enqueueWait(p *packet) bool {
	data := encodePacket(p)
	select {
	case s.send <- data:
		return true
	case <-s.done:
		return false
	}
}

func (s *Session) writeRaw(data []byte) error {
	if s.conn == nil {
		return net.ErrClosed
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(s.broker.opts.WriteTimeout)); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := s.conn.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (s *Session) enqueue(p *packet) bool {
	data := encodePacket(p)
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.send <- data:
		return true
	case <-s.done:
		return false
	default:
		s.broker.log.Warn("broker slow consumer closed",
			logger.String("client_id", s.clientID),
		)
		s.close()
		return false
	}
}

func (s *Session) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.online.Store(false)
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

func (s *Session) active() {
	s.lastActive.Store(time.Now().Unix())
}

// deliver 向在线会话发送消息，返回是否成功入队。
func (s *Session) deliver(msg *Message, qos byte, retain bool) bool {
	s.mu.Lock()
	if !s.online.Load() {
		s.mu.Unlock()
		return false
	}
	packetID := uint16(0)
	if qos > 0 {
		packetID = s.nextPacketIDLocked()
		if packetID == 0 {
			qos = 0
		} else {
			s.outbound[packetID] = outboundState{
				qos:     qos,
				message: *cloneMessage(msg),
				sentAt:  time.Now(),
			}
		}
	}
	p := buildPublish(msg.Topic, msg.Payload, qos, retain, false, packetID)
	ok := s.enqueue(p)
	s.mu.Unlock()

	if ok {
		s.broker.stats.messagesOut.Add(1)
	}
	return ok
}

func (s *Session) nextPacketIDLocked() uint16 {
	for i := 0; i < 65535; i++ {
		s.nextID++
		if s.nextID == 0 {
			continue
		}
		if _, exists := s.outbound[s.nextID]; !exists {
			return s.nextID
		}
	}
	return 0
}

func (s *Session) addSubscription(filter string, qos byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[filter]; ok {
		s.subs[filter] = qos
		return true
	}
	if s.broker.opts.MaxSubscriptions > 0 && len(s.subs) >= s.broker.opts.MaxSubscriptions {
		return false
	}
	s.subs[filter] = qos
	return true
}

func (s *Session) removeSubscription(filter string) {
	s.mu.Lock()
	delete(s.subs, filter)
	s.mu.Unlock()
}

func (s *Session) enqueueOffline(msg *Message, qos byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broker.opts.MaxQueuedMessages > 0 && len(s.pending) >= s.broker.opts.MaxQueuedMessages {
		s.pending = s.pending[1:]
		s.broker.stats.messagesDropped.Add(1)
	}
	s.pending = append(s.pending, pendingMessage{message: *cloneMessage(msg), qos: qos})
}

func (s *Session) takePending() []pendingMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending
	s.pending = nil
	return pending
}

func (s *Session) copyStateFrom(old *Session) {
	if old == nil {
		return
	}
	old.mu.RLock()
	defer old.mu.RUnlock()
	for filter, qos := range old.subs {
		s.subs[filter] = qos
	}
	s.pending = append(s.pending, old.pending...)
	// 持久会话必须继承未确认的 QoS 消息，重连后按 DUP 重发。
	for id, state := range old.outbound {
		cloned := state
		cloned.message = *cloneMessage(&state.message)
		s.outbound[id] = cloned
	}
	if old.nextID > s.nextID {
		s.nextID = old.nextID
	}
	// 入站 QoS2 去重状态同样属于会话状态，避免重连后重复投递。
	for id := range old.inboundQoS2 {
		s.inboundQoS2[id] = struct{}{}
	}
}

func (s *Session) markInboundQoS2(id uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inboundQoS2[id]; exists {
		return false
	}
	s.inboundQoS2[id] = struct{}{}
	return true
}

func (s *Session) clearInboundQoS2(id uint16) {
	s.mu.Lock()
	delete(s.inboundQoS2, id)
	s.mu.Unlock()
}

func (s *Session) handlePuback(id uint16) {
	s.mu.Lock()
	delete(s.outbound, id)
	s.mu.Unlock()
}

func (s *Session) handlePubrec(id uint16) {
	s.mu.Lock()
	if state, ok := s.outbound[id]; ok {
		state.pubrelSent = true
		state.sentAt = time.Now()
		s.outbound[id] = state
	}
	s.mu.Unlock()
	s.enqueue(buildAck(packetPubrel, id))
}

func (s *Session) handlePubcomp(id uint16) {
	s.mu.Lock()
	delete(s.outbound, id)
	s.mu.Unlock()
}

func cloneMessage(msg *Message) *Message {
	if msg == nil {
		return nil
	}
	cloned := *msg
	cloned.Payload = append([]byte(nil), msg.Payload...)
	return &cloned
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
