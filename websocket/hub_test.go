package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
)

func newTestHub(t *testing.T, opts Options) (*Hub, *httptest.Server) {
	t.Helper()
	opts.Logger = logger.NewNop()
	hub := NewHub(opts)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", hub.Handler())
	server := httptest.NewServer(engine)
	t.Cleanup(func() {
		_ = hub.Close()
		waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hub.Wait(waitCtx)
		server.Close()
	})
	return hub, server
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
}

func dial(t *testing.T, server *httptest.Server, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(wsURL(server), headers)
}

func TestBroadcastAndSend(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	if hub.Stats().Clients != 1 {
		t.Fatalf("clients = %d", hub.Stats().Clients)
	}

	if sent := hub.Broadcast([]byte("broadcast")); sent != 1 {
		t.Fatalf("broadcast sent = %d", sent)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read broadcast: %v", err)
	}
	if string(payload) != "broadcast" {
		t.Fatalf("payload = %q", payload)
	}

	if sent := hub.Send("anonymous", []byte("direct")); sent != 1 {
		t.Fatalf("send = %d", sent)
	}
	_, payload, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read direct: %v", err)
	}
	if string(payload) != "direct" {
		t.Fatalf("payload = %q", payload)
	}
	if sent := hub.Send("nobody", []byte("x")); sent != 0 {
		t.Fatalf("send to unknown subject = %d", sent)
	}
}

func TestAuthenticationRequired(t *testing.T) {
	_, server := newTestHub(t, Options{AllowAnonymous: false})

	_, resp, err := dial(t, server, "")
	if err == nil {
		t.Fatal("expected handshake failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v", resp)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestAuthenticatorSubject(t *testing.T) {
	linker := func(*gin.Context) (string, error) { return "user-1", nil }
	hub, server := newTestHub(t, Options{Authenticate: linker})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	waitFor(t, func() bool {
		clients := hub.Clients()
		return len(clients) == 1 && clients[0].Subject == "user-1"
	})
	hub.Send("user-1", []byte("hello"))
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "hello" {
		t.Fatalf("read = %q, %v", payload, err)
	}
}

func TestAuthenticatorRejects(t *testing.T) {
	linker := func(*gin.Context) (string, error) {
		return "", apperrors.Unauthorized("invalid token")
	}
	_, server := newTestHub(t, Options{Authenticate: linker})

	_, resp, err := dial(t, server, "")
	if err == nil {
		t.Fatal("expected handshake failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v", resp)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestOriginPolicy(t *testing.T) {
	_, server := newTestHub(t, Options{AllowAnonymous: true, AllowedOrigins: []string{"https://app.example.com"}})

	allowed, _, err := dial(t, server, "https://app.example.com")
	if err != nil {
		t.Fatalf("allowed origin dial: %v", err)
	}
	_ = allowed.Close()

	_, resp, err := dial(t, server, "https://evil.example.com")
	if err == nil {
		t.Fatal("expected origin rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v", resp)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestOnMessagePanicIsIsolated(t *testing.T) {
	hub, server := newTestHub(t, Options{
		AllowAnonymous: true,
		OnMessage: func(context.Context, string, []byte) error {
			panic("boom")
		},
	})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("trigger")); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if hub.Stats().Clients != 1 {
		t.Fatalf("expected connection to stay alive, clients = %d", hub.Stats().Clients)
	}

	hub.Broadcast([]byte("alive"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "alive" {
		t.Fatalf("read = %q, %v", payload, err)
	}
}

func TestCloseDisconnectsClients(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := hub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hub.Wait(waitCtx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if hub.Stats().Closed != true || hub.Stats().Clients != 0 {
		t.Fatalf("unexpected stats: %+v", hub.Stats())
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection to be closed")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

type testFrame struct {
	Type    string `json:"type"`
	Topic   string `json:"topic"`
	Message string `json:"message"`
}

func writeJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) testFrame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var frame testFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("frame %q: %v", payload, err)
	}
	return frame
}

func onlyClient(t *testing.T, hub *Hub) *Client {
	t.Helper()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.clients) != 1 {
		t.Fatalf("clients = %d", len(hub.clients))
	}
	for client := range hub.clients {
		return client
	}
	return nil
}

func sameTopics(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestTopicSubscribePublishUnsubscribe(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	writeJSON(t, conn, map[string]any{"type": "subscribe", "topic": "news"})
	if frame := readFrame(t, conn); frame.Type != "subscribed" || frame.Topic != "news" {
		t.Fatalf("subscribe frame = %+v", frame)
	}

	if sent := hub.Publish("news", []byte("hello")); sent != 1 {
		t.Fatalf("publish sent = %d", sent)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "hello" {
		t.Fatalf("read publish = %q, %v", payload, err)
	}

	client := onlyClient(t, hub)
	if topics := client.Topics(); !sameTopics(topics, "news") {
		t.Fatalf("client topics = %v", topics)
	}
	if topics := hub.Topics(); !sameTopics(topics, "news") {
		t.Fatalf("hub topics = %v", topics)
	}

	if sent := hub.Subscribe("anonymous", "lobby"); sent != 1 {
		t.Fatalf("hub subscribe = %d", sent)
	}
	if topics := hub.Topics(); !sameTopics(topics, "lobby", "news") {
		t.Fatalf("hub topics after subscribe = %v", topics)
	}
	if sent := hub.Unsubscribe("anonymous", "lobby"); sent != 1 {
		t.Fatalf("hub unsubscribe = %d", sent)
	}

	writeJSON(t, conn, map[string]any{"type": "unsubscribe", "topic": "news"})
	if frame := readFrame(t, conn); frame.Type != "unsubscribed" || frame.Topic != "news" {
		t.Fatalf("unsubscribe frame = %+v", frame)
	}
	if sent := hub.Publish("news", []byte("ignored")); sent != 0 {
		t.Fatalf("publish after unsubscribe sent = %d", sent)
	}
	if topics := hub.Topics(); len(topics) != 0 {
		t.Fatalf("hub topics after unsubscribe = %v", topics)
	}

	writeJSON(t, conn, map[string]any{"type": "ping"})
	if frame := readFrame(t, conn); frame.Type != "pong" {
		t.Fatalf("ping frame = %+v", frame)
	}
}

func TestTopicLimit(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, MaxTopics: 2})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	for _, topic := range []string{"a", "b"} {
		writeJSON(t, conn, map[string]any{"type": "subscribe", "topic": topic})
		if frame := readFrame(t, conn); frame.Type != "subscribed" || frame.Topic != topic {
			t.Fatalf("subscribe %s frame = %+v", topic, frame)
		}
	}

	writeJSON(t, conn, map[string]any{"type": "subscribe", "topic": "c"})
	frame := readFrame(t, conn)
	if frame.Type != "error" || !strings.Contains(frame.Message, "limit") {
		t.Fatalf("over-limit frame = %+v", frame)
	}
	if topics := hub.Topics(); !sameTopics(topics, "a", "b") {
		t.Fatalf("topics = %v", topics)
	}
}

func TestControlInvalidJSON(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not-json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if frame := readFrame(t, conn); frame.Type != "error" || !strings.Contains(frame.Message, "json") {
		t.Fatalf("invalid json frame = %+v", frame)
	}

	writeJSON(t, conn, map[string]any{"type": "ping"})
	if frame := readFrame(t, conn); frame.Type != "pong" {
		t.Fatalf("ping after invalid json = %+v", frame)
	}
}

func TestControlPublishRoutesToSubscribers(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})

	subscriber, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial subscriber: %v", err)
	}
	defer subscriber.Close()
	publisher, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial publisher: %v", err)
	}
	defer publisher.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 2 })

	writeJSON(t, subscriber, map[string]any{"type": "subscribe", "topic": "chat"})
	if frame := readFrame(t, subscriber); frame.Type != "subscribed" || frame.Topic != "chat" {
		t.Fatalf("subscribe frame = %+v", frame)
	}

	writeJSON(t, publisher, map[string]any{
		"type":  "publish",
		"topic": "chat",
		"data":  map[string]any{"msg": "hi"},
	})

	_ = subscriber.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := subscriber.ReadMessage()
	if err != nil {
		t.Fatalf("read published: %v", err)
	}
	if string(payload) != `{"msg":"hi"}` {
		t.Fatalf("published payload = %q", payload)
	}
}

func TestOnMessageBypassesBuiltinProtocol(t *testing.T) {
	received := make(chan string, 1)
	_, server := newTestHub(t, Options{
		AllowAnonymous: true,
		OnMessage: func(_ context.Context, _ string, payload []byte) error {
			received <- string(payload)
			return nil
		},
	})

	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	writeJSON(t, conn, map[string]any{"type": "ping"})
	select {
	case payload := <-received:
		if payload != `{"type":"ping"}` {
			t.Fatalf("handler payload = %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not receive message")
	}
}

func TestDefaultMaxTopics(t *testing.T) {
	if got := DefaultConfig().MaxTopics; got != 100 {
		t.Fatalf("default max topics = %d", got)
	}
}
