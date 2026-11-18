package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

func TestOriginAllowed(t *testing.T) {
	newRequest := func(origin, host string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		return req
	}

	if !originAllowed(newRequest("", "example.com"), nil) {
		t.Fatal("empty origin should be allowed")
	}
	if !originAllowed(newRequest("https://example.com", "example.com"), []string{"*"}) {
		t.Fatal("wildcard should allow any origin")
	}
	if !originAllowed(newRequest("https://example.com/", "example.com"), []string{" https://example.com/ "}) {
		t.Fatal("trailing slash should be normalized")
	}
	if !originAllowed(newRequest("https://example.com", "example.com"), []string{"", "https://example.com"}) {
		t.Fatal("blank entries should be skipped")
	}
	if originAllowed(newRequest("https://evil.example.com", "example.com"), []string{"https://example.com"}) {
		t.Fatal("unlisted origin must be rejected")
	}
	if !originAllowed(newRequest("http://example.com", "example.com"), nil) {
		t.Fatal("same-host origin should be allowed")
	}
	if originAllowed(newRequest("http://other.example.com", "example.com"), nil) {
		t.Fatal("cross-host origin must be rejected")
	}
	if originAllowed(newRequest("http://[::1", "example.com"), nil) {
		t.Fatal("invalid origin must be rejected")
	}
}

func TestHandlerRejectsWhenClosed(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})
	if err := hub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	resp, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHandlerUpgradeFailure(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true})
	resp, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if hub.Stats().Clients != 0 {
		t.Fatalf("clients = %d, want 0", hub.Stats().Clients)
	}
}

func TestEmptyAuthenticatedSubjectFallsBackToAnonymous(t *testing.T) {
	hub, server := newTestHub(t, Options{
		Authenticate: func(*gin.Context) (string, error) { return "", nil },
	})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	waitFor(t, func() bool {
		clients := hub.Clients()
		return len(clients) == 1 && clients[0].Subject == "anonymous"
	})
}

func TestHubPublishAndSubscribeValidation(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, MaxTopics: 1})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })

	if sent := hub.Publish("   ", []byte("x")); sent != 0 {
		t.Fatalf("publish empty topic sent = %d", sent)
	}
	if sent := hub.Subscribe("anonymous", "  "); sent != 0 {
		t.Fatalf("subscribe empty topic sent = %d", sent)
	}
	if sent := hub.Unsubscribe("anonymous", "  "); sent != 0 {
		t.Fatalf("unsubscribe empty topic sent = %d", sent)
	}
	if sent := hub.Subscribe("anonymous", "a"); sent != 1 {
		t.Fatalf("subscribe sent = %d", sent)
	}
	// 超过 MaxTopics，Hub.Subscribe 记录失败并不计数。
	if sent := hub.Subscribe("anonymous", "b"); sent != 0 {
		t.Fatalf("subscribe over limit sent = %d", sent)
	}
	if sent := hub.Unsubscribe("anonymous", "missing"); sent != 0 {
		t.Fatalf("unsubscribe missing sent = %d", sent)
	}
}

func TestClientTopicValidation(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, MaxTopics: 1})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitFor(t, func() bool { return hub.Stats().Clients == 1 })
	client := onlyClient(t, hub)

	if err := client.Subscribe("  "); err == nil {
		t.Fatal("expected topic required error")
	}
	if err := client.Subscribe("one"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := client.Subscribe(" one "); err != nil {
		t.Fatalf("duplicate Subscribe: %v", err)
	}
	if err := client.Subscribe("two"); err == nil {
		t.Fatal("expected topic limit error")
	}
	if client.Unsubscribe("  ") {
		t.Fatal("empty unsubscribe must return false")
	}
	if client.Unsubscribe("missing") {
		t.Fatal("missing unsubscribe must return false")
	}
	if !client.Unsubscribe("one") {
		t.Fatal("expected unsubscribe to succeed")
	}
}

func TestEnqueueClosesSlowConsumer(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, SendQueue: 1, InboundQueue: 1})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := newClient(hub, conn, "manual")
	if !client.enqueue([]byte("first")) {
		t.Fatal("first enqueue should succeed")
	}
	if client.enqueue([]byte("second")) {
		t.Fatal("second enqueue should close slow consumer")
	}
	if client.enqueue([]byte("third")) {
		t.Fatal("enqueue on closed client must fail")
	}

	inbound := newClient(hub, conn, "inbound")
	if !inbound.enqueueInbound([]byte("first")) {
		t.Fatal("first inbound should succeed")
	}
	if inbound.enqueueInbound([]byte("second")) {
		t.Fatal("second inbound should close slow consumer")
	}
	if inbound.enqueueInbound([]byte("third")) {
		t.Fatal("enqueueInbound on closed client must fail")
	}
}

func TestInboundQueueFullClosesConnection(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	hub, server := newTestHub(t, Options{
		AllowAnonymous: true,
		InboundQueue:   1,
		OnMessage: func(context.Context, string, []byte) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-block
			return errors.New("handler failed")
		},
	})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	for i := 0; i < 3; i++ {
		if err := conn.WriteMessage(1, []byte("payload")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
	waitFor(t, func() bool { return hub.Stats().Clients == 0 })
	close(block)
}

func TestPingIntervalKeepsConnectionAlive(t *testing.T) {
	hub, server := newTestHub(t, Options{AllowAnonymous: true, PingInterval: 10 * time.Millisecond})
	conn, _, err := dial(t, server, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)
	if hub.Stats().Clients != 1 {
		t.Fatalf("clients = %d, want 1", hub.Stats().Clients)
	}
	hub.Broadcast([]byte("alive"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "alive" {
		t.Fatalf("read = %q, %v", payload, err)
	}
}

func TestWaitTimeoutAndAddWhenClosed(t *testing.T) {
	hub := NewHub(Options{Logger: logger.NewNop()})
	hub.wg.Add(2)
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := hub.Wait(waitCtx); err == nil {
		t.Fatal("expected wait timeout")
	}

	if err := hub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if hub.add(newClient(hub, nil, "later"), 2) {
		t.Fatal("add must fail when hub is closed")
	}
	hub.wg.Done()
	hub.wg.Done()
}

func TestBootLifecycle(t *testing.T) {
	_ = os.Setenv("GOFRAME_WEBSOCKET_ENABLED", "true")
	_ = os.Setenv("GOFRAME_WEBSOCKET_PATH", "/ws")
	_ = os.Setenv("GOFRAME_WEBSOCKET_ALLOW_ANONYMOUS", "true")
	defer func() {
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_ENABLED")
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_PATH")
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_ALLOW_ANONYMOUS")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}

	SetAuthenticator(func(*gin.Context) (string, error) { return "boot-user", nil })
	SetOnMessage(func(context.Context, string, []byte) error { return nil })
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("expected global hub")
	}
	if err := Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil): %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil hub after Shutdown")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	SetAuthenticator(nil)
	SetOnMessage(nil)
}

func TestBootDisabled(t *testing.T) {
	_ = os.Setenv("GOFRAME_WEBSOCKET_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_ENABLED")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled hub must not be created")
	}
}

func TestBootInvalidPath(t *testing.T) {
	_ = os.Setenv("GOFRAME_WEBSOCKET_ENABLED", "true")
	_ = os.Setenv("GOFRAME_WEBSOCKET_PATH", "ws")
	defer func() {
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_ENABLED")
		_ = os.Unsetenv("GOFRAME_WEBSOCKET_PATH")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected path validation error")
	}
}
