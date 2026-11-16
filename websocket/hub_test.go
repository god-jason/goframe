package websocket

import (
	"context"
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
