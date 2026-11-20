package broker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

// scriptedConn 是可控的 net.Conn，用于直接驱动 handleConn / writeRaw 的错误分支。
type scriptedConn struct {
	r              io.Reader
	writeErr       bool // Write 返回错误
	wDeadlineErr   bool // SetWriteDeadline 返回错误
	readDeadlineAt int  // SetReadDeadline 调用次数超过该值后返回错误
	readDeadlineN  int
	closed         atomic.Bool
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	if c.r == nil {
		return 0, io.EOF
	}
	return c.r.Read(p)
}

func (c *scriptedConn) Write(p []byte) (int, error) {
	if c.writeErr {
		return 0, errors.New("write boom")
	}
	return len(p), nil
}

func (c *scriptedConn) Close() error { c.closed.Store(true); return nil }
func (c *scriptedConn) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *scriptedConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *scriptedConn) SetDeadline(time.Time) error { return nil }

func (c *scriptedConn) SetReadDeadline(time.Time) error {
	c.readDeadlineN++
	if c.readDeadlineN > c.readDeadlineAt {
		return errors.New("read deadline boom")
	}
	return nil
}

func (c *scriptedConn) SetWriteDeadline(time.Time) error {
	if c.wDeadlineErr {
		return errors.New("write deadline boom")
	}
	return nil
}

// connectBytes 构造一个合法 CONNECT 报文用于 scriptedConn 的读流。
func connectBytes(clientID string, clean bool) []byte {
	spec := connectSpec{clientID: clientID, clean: clean, keepAlive: 30}
	return encodePacket(spec.packet())
}

func newNopServer(opts Options) *Server {
	opts.Logger = logger.NewNop()
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	return New(opts)
}

// TestStartShutdownNilContext 覆盖 Start/Shutdown 中 ctx==nil 的分支。
func TestStartShutdownNilContext(t *testing.T) {
	server := newNopServer(Options{})
	if err := server.Start(nil); err != nil {
		t.Fatalf("Start(nil): %v", err)
	}
	if err := server.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil): %v", err)
	}
}

// TestAcceptLoopNilContext 覆盖 acceptLoop 中 s.ctx==nil 时使用 Background 的分支。
func TestAcceptLoopNilContext(t *testing.T) {
	server := newNopServer(Options{})
	// s.ctx 保持 nil，acceptLoop 进入时回退到 context.Background()。
	listener := &oneConnListener{
		conn:   &scriptedConn{r: bytes.NewReader(nil)},
		blocked: make(chan struct{}),
	}
	server.wg.Add(1)
	go server.acceptLoop(listener)
	time.Sleep(100 * time.Millisecond)
	// 让 acceptLoop 中的第二次 Accept 阻塞，避免后台 goroutine 空转。
	close(listener.blocked)
}

// oneConnListener 第一次返回一个立即 EOF 的连接，第二次返回错误，之后阻塞。
type oneConnListener struct {
	conn    net.Conn
	calls   atomic.Int32
	blocked chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	n := l.calls.Add(1)
	switch {
	case n == 1:
		return l.conn, nil
	case n == 2:
		return nil, errors.New("accept failed")
	default:
		<-l.blocked
		return nil, errors.New("accept failed")
	}
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// TestAcceptLoopBackoffCap 覆盖退避时间达到上限（time.Second）的分支。
func TestAcceptLoopBackoffCap(t *testing.T) {
	server := newNopServer(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	server.ctx = ctx
	listener := &failingListener{closed: make(chan struct{})}
	server.wg.Add(1)
	done := make(chan struct{})
	go func() {
		server.acceptLoop(listener)
		close(done)
	}()
	// 50+100+200+400+800=1550ms 后第 6 次失败将退避封顶为 1s。
	time.Sleep(1700 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not stop")
	}
	if listener.calls.Load() < 6 {
		t.Fatalf("accept calls = %d, want >= 6", listener.calls.Load())
	}
}

// TestHandleConnAutoClientID 覆盖空 ClientID 且 clean 时自动生成 ID 的分支。
func TestHandleConnAutoClientID(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "", clean: true, keepAlive: 30})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d, want 0", code)
	}
	_ = reader
	waitFor(t, "auto id online", func() bool { return server.Stats().Online == 1 })
}

// TestHandleConnRegisterFails 覆盖 register 返回错误时回送 CONNACK 0x03 并断开的分支。
func TestHandleConnRegisterFails(t *testing.T) {
	server := newNopServer(Options{AllowAnonymous: true, MaxClients: 1})
	server.online.Store(1) // 模拟已满，accept 检查被绕过（直接调用 handleConn）。
	conn := &scriptedConn{r: bytes.NewReader(connectBytes("full-client", true))}
	server.wg.Add(1)
	server.handleConn(conn)
	if !conn.closed.Load() {
		t.Fatal("expected connection closed after register failure")
	}
}

// TestHandleConnReadDeadlineError 覆盖主循环中 SetReadDeadline 失败的分支。
func TestHandleConnReadDeadlineError(t *testing.T) {
	server := newNopServer(Options{AllowAnonymous: true})
	// 第 1 次 SetReadDeadline（连接阶段）成功，第 2 次（循环阶段）失败。
	conn := &scriptedConn{r: bytes.NewReader(connectBytes("dl-client", true)), readDeadlineAt: 1}
	server.wg.Add(1)
	server.handleConn(conn)
	if !conn.closed.Load() {
		t.Fatal("expected connection closed after deadline error")
	}
}

// TestRawAckParseErrors 覆盖 PUBREC/PUBREL/PUBCOMP 报文 ID 解析失败分支。
func TestRawAckParseErrors(t *testing.T) {
	server := newTestBroker(t, Options{})
	cases := map[string]*packet{
		"pubrec short":  {typ: packetPubrec, payload: []byte{0}},
		"pubrel short":  {typ: packetPubrel, flags: 0x02, payload: []byte{0}},
		"pubcomp short": {typ: packetPubcomp, payload: []byte{0}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: name, clean: true})
			defer conn.Close()
			if code != 0 {
				t.Fatalf("connack code = %d", code)
			}
			if _, err := conn.Write(encodePacket(p)); err != nil {
				t.Fatalf("write: %v", err)
			}
			expectClosed(t, conn, reader)
		})
	}
}

// TestRawUnsubscribeInvalidFilter 覆盖 UNSUBSCRIBE 中 validateFilter 失败分支。
func TestRawUnsubscribeInvalidFilter(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "unsub-bad", clean: true})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	if _, err := conn.Write(encodePacket(buildUnsubscribePacket(1, "a/#/b"))); err != nil {
		t.Fatalf("write: %v", err)
	}
	expectClosed(t, conn, reader)
}

// TestRouteOfflineQueueWhenDeliverFails 覆盖在线会话投递失败且为持久会话时进入离线队列的分支。
func TestRouteOfflineQueueWhenDeliverFails(t *testing.T) {
	server := newNopServer(Options{SendQueue: 1})
	session := newSession(1, server, &ConnectRequest{ClientID: "slow-persistent", CleanSession: false}, &scriptedConn{})
	// 预先填满发送队列，使 enqueue 走入慢消费者分支返回 false。
	session.send <- []byte("block")

	server.mu.Lock()
	server.sessions["slow-persistent"] = session
	server.indexSubscribeLocked(session, "slow/#", 1)
	server.mu.Unlock()

	server.route(&Message{Topic: "slow/1", Payload: []byte("a"), QoS: 1}, nil)

	session.mu.Lock()
	pending := len(session.pending)
	session.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}
}

// TestDeliverRetainedNonMatchingFilter 覆盖 deliverRetained 中主题不匹配 continue 的分支。
func TestDeliverRetainedNonMatchingFilter(t *testing.T) {
	server := newTestBroker(t, Options{})
	if err := server.Publish("keep/retained", []byte("v"), 0, true); err != nil {
		t.Fatalf("publish retained: %v", err)
	}
	waitFor(t, "retained stored", func() bool { return server.Stats().Retained == 1 })

	subscriber := connectPaho(t, server.Addr(), "retain-nomatch", true, nil)
	// 订阅一个不匹配已保留主题的过滤器。
	waitToken(t, subscriber.Subscribe("other/#", 0, nil))
}

// TestCleanupSessionsOnlineAndNoExpiry 覆盖 cleanupSessions 在线 continue 与过期时间<=0 分支。
func TestCleanupSessionsOnlineAndNoExpiry(t *testing.T) {
	server := newTestBroker(t, Options{SessionExpiry: time.Millisecond})
	connectPaho(t, server.Addr(), "online-expire", false, nil)
	waitFor(t, "online", func() bool { return server.Stats().Online == 1 })
	// 在线会话应被 continue 跳过。
	server.cleanupSessions()
	if server.Stats().Clients == 0 {
		t.Fatal("online session must not be cleaned")
	}

	noExpiry := newNopServer(Options{})
	noExpiry.opts.SessionExpiry = 0
	noExpiry.cleanupSessions()
}

// TestRetransmitLoopZeroInterval 覆盖 retransmitLoop 中重发间隔<=0 回退默认值的分支。
func TestRetransmitLoopZeroInterval(t *testing.T) {
	server := newNopServer(Options{})
	server.opts.RetransmitInterval = 0
	session := newSession(1, server, &ConnectRequest{ClientID: "rt", CleanSession: true}, &scriptedConn{})
	server.wg.Add(1)
	go session.retransmitLoop()
	time.Sleep(20 * time.Millisecond)
	session.close()
}

// TestRetransmitPubrelAndBatchAndEnqueueFail 覆盖 retransmit 的 PUBREL 重发、批量上限与入队失败分支。
func TestRetransmitPubrelAndBatchAndEnqueueFail(t *testing.T) {
	server := newNopServer(Options{SendQueue: 128})

	t.Run("pubrel branch", func(t *testing.T) {
		session := newSession(1, server, &ConnectRequest{ClientID: "rt-pubrel", CleanSession: true}, &scriptedConn{})
		session.mu.Lock()
		session.outbound[1] = outboundState{qos: 2, pubrelSent: true, sentAt: time.Now().Add(-time.Hour), message: Message{Topic: "t"}}
		session.mu.Unlock()
		session.retransmit(time.Second, time.Now())
	})

	t.Run("batch limit", func(t *testing.T) {
		session := newSession(2, server, &ConnectRequest{ClientID: "rt-batch", CleanSession: true}, &scriptedConn{})
		session.mu.Lock()
		for i := 1; i <= 70; i++ {
			session.outbound[uint16(i)] = outboundState{qos: 1, sentAt: time.Now().Add(-time.Hour), message: Message{Topic: "t"}}
		}
		session.mu.Unlock()
		session.retransmit(time.Second, time.Now())
	})

	t.Run("enqueue fail", func(t *testing.T) {
		session := newSession(3, server, &ConnectRequest{ClientID: "rt-fail", CleanSession: true}, &scriptedConn{})
		session.mu.Lock()
		session.outbound[9] = outboundState{qos: 1, sentAt: time.Now().Add(-time.Hour), message: Message{Topic: "t"}}
		session.mu.Unlock()
		session.close() // done 关闭，enqueue 返回 false
		session.retransmit(time.Second, time.Now())
	})
}

// TestReplayOutboundEnqueueWaitFail 覆盖 replayOutbound 中 enqueueWait 失败返回的分支。
func TestReplayOutboundEnqueueWaitFail(t *testing.T) {
	server := newNopServer(Options{})
	session := newSession(1, server, &ConnectRequest{ClientID: "replay-fail", CleanSession: true}, &scriptedConn{})
	session.mu.Lock()
	session.outbound[5] = outboundState{qos: 1, sentAt: time.Now(), message: Message{Topic: "t"}}
	session.mu.Unlock()
	session.close()
	session.replayOutbound()
}

// TestWriteRawWriteError 覆盖 writeRaw 中 Write 返回错误的分支。
func TestWriteRawWriteError(t *testing.T) {
	server := newNopServer(Options{})
	session := newSession(1, server, &ConnectRequest{ClientID: "wr", CleanSession: true}, &scriptedConn{writeErr: true})
	if err := session.writeRaw([]byte("data")); err == nil {
		t.Fatal("expected write error")
	}
}

// TestReadPacketRemainingLengthError 覆盖 readRemainingLength 读取续字节失败的分支。
func TestReadPacketRemainingLengthError(t *testing.T) {
	if _, err := readPacket(bufio.NewReader(bytes.NewReader([]byte{0x30})), 1<<20); err == nil {
		t.Fatal("expected remaining length read error")
	}
}

// TestParseConnectTruncatedFields 覆盖 parseConnect 各字段读取失败的分支。
func TestParseConnectTruncatedFields(t *testing.T) {
	prefix := []byte{0, 4, 'M', 'Q', 'T', 'T'}
	cases := map[string][]byte{
		"level":       append([]byte(nil), prefix...),
		"flags":       append(append([]byte(nil), prefix...), protocolLevel311),
		"keepalive":   append(append([]byte(nil), prefix...), protocolLevel311, 0x02),
		"clientid":    append(append([]byte(nil), prefix...), protocolLevel311, 0x02, 0, 30),
		"will topic":  append(append([]byte(nil), prefix...), protocolLevel311, 0x06, 0, 30, 0, 2, 'c', '1'),
		"will message": append(append(append([]byte(nil), prefix...), protocolLevel311, 0x06, 0, 30, 0, 2, 'c', '1'), 0, 1, 'w'),
		"username":    append(append([]byte(nil), prefix...), protocolLevel311, 0x82, 0, 30, 0, 2, 'c', '1'),
		"password":    append(append(append([]byte(nil), prefix...), protocolLevel311, 0xC2, 0, 30, 0, 2, 'c', '1'), 0, 1, 'u'),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConnect(payload); err == nil {
				t.Fatalf("expected parse error for %s", name)
			}
		})
	}
}

// TestParsePublishTruncatedTopic 覆盖 parsePublish 读取主题失败的分支。
func TestParsePublishTruncatedTopic(t *testing.T) {
	if _, err := parsePublish(&packet{typ: packetPublish, payload: []byte{0}}); err == nil {
		t.Fatal("expected topic read error")
	}
}

// TestParseSubscribeTruncatedFilter 覆盖 parseSubscribe 读取过滤器失败的分支。
func TestParseSubscribeTruncatedFilter(t *testing.T) {
	p := &packet{typ: packetSubscribe, flags: 0x02, payload: []byte{0, 1, 0, 3, 'a'}}
	if _, _, err := parseSubscribe(p); err == nil {
		t.Fatal("expected filter read error")
	}
}

// TestBootStartupWithoutAuthenticator 覆盖 Startup 中未配置鉴权且禁止匿名时的警告分支。
func TestBootStartupWithoutAuthenticator(t *testing.T) {
	_ = os.Setenv("GOFRAME_BROKER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_BROKER_ADDR", "127.0.0.1:0")
	_ = os.Setenv("GOFRAME_BROKER_ALLOW_ANONYMOUS", "false")
	defer func() {
		_ = os.Unsetenv("GOFRAME_BROKER_ENABLED")
		_ = os.Unsetenv("GOFRAME_BROKER_ADDR")
		_ = os.Unsetenv("GOFRAME_BROKER_ALLOW_ANONYMOUS")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	SetAuthenticator(nil)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = Shutdown(ctx)
}

// 说明：以下分支在当前实现下逻辑不可达，保留作为防御性代码：
//   - broker.go 432-435：ClientID 为空且非 clean 时 parseConnect 已先行拒绝，
//     而空 ID 且 clean 会在 429 行自动补全，故 431 行条件永不为真。
//   - broker.go 826：maintenanceLoop 的 ticker 周期固定为 1 分钟，测试无法在合理时间内触发。
//   - session.go 248：enqueue 第二个 select 中 done 关闭的竞态分支，无法确定性触发。
