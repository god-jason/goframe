package websocket

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/logger"
)

// TestNewHubDefaults 覆盖 NewHub 中 Logger 为空时回退 Nop 的分支。
func TestNewHubDefaults(t *testing.T) {
	hub := NewHub(Options{})
	if hub.log == nil {
		t.Fatal("expected default logger")
	}
	if hub.opts.MaxTopics != 100 || hub.opts.SendQueue != 256 || hub.opts.InboundQueue != 64 {
		t.Fatalf("unexpected defaults: %+v", hub.opts)
	}
}

// TestControlUnknownType 覆盖 handleControl 默认分支（未知类型）。
func TestControlUnknownType(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	writeJSON(t, conn, map[string]any{"type": "bogus"})
	if frame := readFrame(t, conn); frame.Type != "error" || !strings.Contains(frame.Message, "unknown") {
		t.Fatalf("unknown type frame = %+v", frame)
	}
}

// TestControlPublishEmptyTopic 覆盖 handleControl publish 分支中空主题的早退。
func TestControlPublishEmptyTopic(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	writeJSON(t, conn, map[string]any{"type": "publish", "topic": "   "})
	if frame := readFrame(t, conn); frame.Type != "error" || !strings.Contains(frame.Message, "topic") {
		t.Fatalf("publish empty topic frame = %+v", frame)
	}
}

// TestControlPanicRecovered 覆盖 handleControl 的 panic 隔离分支。
// topics 为 nil 的客户端在订阅写入时触发 panic，被 recover 捕获。
func TestControlPanicRecovered(t *testing.T) {
	hub := NewHub(Options{Logger: logger.NewNop()})
	client := &Client{
		hub:     hub,
		closed:  make(chan struct{}),
		send:    make(chan []byte, 1),
		inbound: make(chan []byte, 1),
	}
	client.handleControl([]byte(`{"type":"subscribe","topic":"x"}`))
}

// TestWritePumpSendError 覆盖 writePump 写出数据帧失败返回的分支。
func TestWritePumpSendError(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := newClient(hub, conn, "wp-send")
	_ = conn.Close() // 关闭底层连接，使后续 WriteMessage 失败

	hub.wg.Add(1)
	done := make(chan struct{})
	go func() {
		client.writePump()
		close(done)
	}()
	client.send <- []byte("boom")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writePump did not exit on send error")
	}
}

// TestWritePumpPingError 覆盖 writePump 心跳写失败返回的分支。
func TestWritePumpPingError(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, PingInterval: 10 * time.Millisecond})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := newClient(hub, conn, "wp-ping")
	_ = conn.Close()

	hub.wg.Add(1)
	done := make(chan struct{})
	go func() {
		client.writePump()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writePump did not exit on ping error")
	}
}

// TestHandlerAddFailsWhenClosedDuringHandshake 覆盖 Handler 中升级后 add 返回 false 的分支：
// 鉴权回调在握手阶段关闭 Hub，使随后 add 检测到已关闭。
func TestHandlerAddFailsWhenClosedDuringHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := NewHub(Options{Logger: logger.NewNop()})
	hub.opts.Authenticate = func(*gin.Context) (string, error) {
		_ = hub.Close()
		return "subject", nil
	}
	engine := gin.New()
	engine.GET("/ws", hub.Handler())
	server := httptest.NewServer(engine)
	defer server.Close()

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 0 })
}

// TestSubscribeDoubleCheckRace 覆盖 subscribe 双重检查命中已存在主题的分支（并发竞态）。
func TestSubscribeDoubleCheckRace(t *testing.T) {
	hub := NewHub(Options{Logger: logger.NewNop(), MaxTopics: 100})
	for i := 0; i < 4000; i++ {
		client := &Client{hub: hub, topics: make(map[string]struct{})}
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = client.subscribe("dup")
			}()
		}
		wg.Wait()
	}
}

// TestEnqueueClosedInSecondSelect 覆盖 enqueue / enqueueInbound 第二 select 中 closed 分支（并发竞态）。
func TestEnqueueClosedInSecondSelect(t *testing.T) {
	hub := NewHub(Options{Logger: logger.NewNop()})
	for i := 0; i < 200; i++ {
		client := &Client{
			hub:     hub,
			send:    make(chan []byte, 1000),
			inbound: make(chan []byte, 1000),
			closed:  make(chan struct{}),
		}
		var wg sync.WaitGroup
		for j := 0; j < 200; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = client.enqueue([]byte("y"))
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = client.enqueueInbound([]byte("y"))
			}()
		}
		time.Sleep(time.Microsecond)
		close(client.closed)
		wg.Wait()
	}
}

// 说明：以下分支为逻辑不可达的防御性代码，保留：
//   - hub.go 598-599：reply 中 json.Marshal 对纯字符串结构不会失败。
//   - boot.go 90-91：Hub.Close 恒返回 nil，Shutdown 中错误分支不可达。
