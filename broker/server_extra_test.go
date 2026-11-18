package broker

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

func waitClients(t *testing.T, server *Server, want int64) {
	t.Helper()
	waitFor(t, "online clients", func() bool { return server.Stats().Online == want })
}

func TestServerPublishAPI(t *testing.T) {
	server := newTestBroker(t, Options{})

	received := make(chan string, 1)
	subscriber := connectPaho(t, server.Addr(), "server-pub-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("server/#", 0, nil))

	if err := server.Publish("server/api", []byte("from-server"), 0, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case payload := <-received:
		if payload != "from-server" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server publish not delivered")
	}

	if err := server.Publish("bad/#", []byte("x"), 0, false); err == nil {
		t.Fatal("expected invalid topic error")
	}
	if err := server.Publish("ok/topic", []byte("x"), 3, false); err == nil {
		t.Fatal("expected invalid qos error")
	}

	if err := server.Publish("retained/api", []byte("value"), 0, true); err != nil {
		t.Fatalf("Publish retained: %v", err)
	}
	if server.Stats().Retained != 1 {
		t.Fatalf("retained = %d, want 1", server.Stats().Retained)
	}
	if err := server.Publish("retained/api", nil, 0, true); err != nil {
		t.Fatalf("Publish retained clear: %v", err)
	}
	if server.Stats().Retained != 0 {
		t.Fatalf("retained = %d, want 0", server.Stats().Retained)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := server.Publish("after/close", nil, 0, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after shutdown = %v, want ErrClosed", err)
	}
}

func TestSessionsSnapshotAndAccessors(t *testing.T) {
	server := newTestBroker(t, Options{})
	client := paho.NewClient(paho.NewClientOptions().
		AddBroker("tcp://" + server.Addr()).
		SetClientID("snapshot-client").
		SetUsername("alice").
		SetCleanSession(true).
		SetKeepAlive(20 * time.Second).
		SetAutoReconnect(false).
		SetConnectTimeout(2 * time.Second))
	token := client.Connect()
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatalf("connect: %v", token.Error())
	}
	t.Cleanup(func() { client.Disconnect(100) })
	waitToken(t, client.Subscribe("snapshot/#", 1, nil))
	waitClients(t, server, 1)

	infos := server.Sessions()
	if len(infos) != 1 {
		t.Fatalf("sessions = %+v", infos)
	}
	info := infos[0]
	if info.ClientID != "snapshot-client" || info.Username != "alice" || !info.Online ||
		!info.CleanSession || info.Subscriptions != 1 || info.RemoteAddr == "" {
		t.Fatalf("unexpected session info: %+v", info)
	}
	if info.ConnectedAt.IsZero() || info.LastActive.IsZero() {
		t.Fatalf("unexpected times: %+v", info)
	}

	server.mu.RLock()
	session := server.sessions["snapshot-client"]
	server.mu.RUnlock()
	if session == nil {
		t.Fatal("session not found")
	}
	if session.ID() == 0 || session.ClientID() != "snapshot-client" || session.Username() != "alice" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if session.RemoteAddr() == "" || !session.CleanSession() || session.KeepAlive() != 20 || !session.Online() {
		t.Fatalf("unexpected session accessors: %+v", session)
	}
	if session.LastActive().IsZero() {
		t.Fatal("expected last active time")
	}
	subs := session.Subscriptions()
	if len(subs) != 1 || subs["snapshot/#"] != 1 {
		t.Fatalf("subscriptions = %+v", subs)
	}
}

func TestQoS2Delivery(t *testing.T) {
	server := newTestBroker(t, Options{})
	subscriber := connectPaho(t, server.Addr(), "qos2-sub", true, nil)
	publisher := connectPaho(t, server.Addr(), "qos2-pub", true, nil)

	received := make(chan string, 1)
	waitToken(t, subscriber.Subscribe("qos2/topic", 2, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	}))
	waitToken(t, publisher.Publish("qos2/topic", 2, false, []byte("exactly-once")))

	select {
	case payload := <-received:
		if payload != "exactly-once" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("qos2 message not delivered")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	server := newTestBroker(t, Options{})
	subscriber := connectPaho(t, server.Addr(), "unsub-sub", true, nil)
	publisher := connectPaho(t, server.Addr(), "unsub-pub", true, nil)

	received := make(chan string, 2)
	waitToken(t, subscriber.Subscribe("unsub/topic", 1, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	}))
	waitToken(t, publisher.Publish("unsub/topic", 1, false, []byte("first")))
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("first message not delivered")
	}

	waitToken(t, subscriber.Unsubscribe("unsub/topic"))
	waitToken(t, publisher.Publish("unsub/topic", 1, false, []byte("second")))
	select {
	case payload := <-received:
		t.Fatalf("unexpected message after unsubscribe: %q", payload)
	case <-time.After(200 * time.Millisecond):
	}
	if stats := server.Stats(); stats.Subscriptions != 0 {
		t.Fatalf("subscriptions = %d, want 0", stats.Subscriptions)
	}
}

func TestRetainedWildcardAndDelete(t *testing.T) {
	server := newTestBroker(t, Options{})
	publisher := connectPaho(t, server.Addr(), "retain-pub", true, nil)
	waitToken(t, publisher.Publish("retain/a", 1, true, []byte("a")))
	waitToken(t, publisher.Publish("retain/b", 1, true, []byte("b")))
	waitFor(t, "retained stored", func() bool { return server.Stats().Retained == 2 })

	// 订阅 QoS 低于 retained QoS，投递时降级。
	received := make(chan string, 4)
	subscriber := connectPaho(t, server.Addr(), "retain-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("retain/+", 0, nil))
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case payload := <-received:
			seen[payload] = true
		case <-time.After(3 * time.Second):
			t.Fatal("retained message not delivered")
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("retained payloads = %v", seen)
	}

	waitToken(t, publisher.Publish("retain/a", 1, true, []byte{}))
	waitFor(t, "retained deleted", func() bool { return server.Stats().Retained == 1 })
}

func TestMaxSubscriptionsReturnsFailureCode(t *testing.T) {
	server := newTestBroker(t, Options{MaxSubscriptions: 1})
	subscriber := connectPaho(t, server.Addr(), "max-sub", true, nil)
	if token := subscriber.Subscribe("one/topic", 0, nil); !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatalf("first subscribe: %v", token.Error())
	}
	token := subscriber.Subscribe("two/topic", 0, nil)
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatalf("second subscribe call: %v", token.Error())
	}
	waitFor(t, "subscription limit", func() bool { return server.Stats().Subscriptions == 1 })
}

func TestMaxQueuedMessagesDropsOldest(t *testing.T) {
	server := newTestBroker(t, Options{MaxQueuedMessages: 1})
	persistent := connectPaho(t, server.Addr(), "drop-queue", false, nil)
	waitToken(t, persistent.Subscribe("drop/#", 1, nil))
	persistent.Disconnect(100)
	waitClients(t, server, 0)

	publisher := connectPaho(t, server.Addr(), "drop-pub", true, nil)
	for i := 0; i < 3; i++ {
		waitToken(t, publisher.Publish("drop/topic", 1, false, []byte{byte('0' + i)}))
	}
	waitFor(t, "messages dropped", func() bool { return server.Stats().MessagesDropped >= 2 })
}

func TestCleanSessionOfflineMessagesAreNotQueued(t *testing.T) {
	server := newTestBroker(t, Options{})
	clean := connectPaho(t, server.Addr(), "clean-offline", true, nil)
	waitToken(t, clean.Subscribe("clean/#", 1, nil))
	clean.Disconnect(100)
	waitClients(t, server, 0)

	publisher := connectPaho(t, server.Addr(), "clean-pub", true, nil)
	waitToken(t, publisher.Publish("clean/topic", 1, false, []byte("x")))
	waitFor(t, "clean session removed", func() bool {
		for _, info := range server.Sessions() {
			if info.ClientID == "clean-offline" {
				return false
			}
		}
		return true
	})
	if stats := server.Stats(); stats.MessagesDropped != 0 {
		t.Fatalf("dropped = %d, want 0", stats.MessagesDropped)
	}
}

func TestHooksInvokedAndPanicIsolated(t *testing.T) {
	var connects, disconnects, publishes, subscribes atomic.Int32
	server := newTestBroker(t, Options{Hooks: Hooks{
		OnConnect:    func(*Session) { connects.Add(1) },
		OnDisconnect: func(*Session, error) { disconnects.Add(1) },
		OnPublish: func(*Session, Message) {
			publishes.Add(1)
			panic("hook boom")
		},
		OnSubscribe: func(*Session, []Subscription) { subscribes.Add(1) },
	}})

	client := connectPaho(t, server.Addr(), "hook-client", true, nil)
	waitToken(t, client.Subscribe("hook/#", 0, nil))
	waitToken(t, client.Publish("hook/topic", 0, false, []byte("x")))
	waitFor(t, "hooks called", func() bool {
		return connects.Load() == 1 && subscribes.Load() == 1 && publishes.Load() == 1
	})
	client.Disconnect(100)
	waitFor(t, "disconnect hook", func() bool { return disconnects.Load() == 1 })
}

func TestCleanupSessions(t *testing.T) {
	server := newTestBroker(t, Options{SessionExpiry: 10 * time.Millisecond})
	persistent := connectPaho(t, server.Addr(), "expiring", false, nil)
	waitToken(t, persistent.Subscribe("expire/#", 1, nil))
	persistent.Disconnect(100)
	waitClients(t, server, 0)

	time.Sleep(30 * time.Millisecond)
	server.cleanupSessions()
	if stats := server.Stats(); stats.Clients != 0 {
		t.Fatalf("clients = %d, want 0", stats.Clients)
	}

	// SessionExpiry <= 0 时不做清理。
	noExpiry := New(Options{Logger: logger.NewNop(), SessionExpiry: -1})
	noExpiry.cleanupSessions()
}

func TestServerLifecycleEdges(t *testing.T) {
	server := New(Options{Logger: logger.NewNop()})
	if server.Addr() != server.opts.Addr {
		t.Fatalf("addr = %q", server.Addr())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}

	if err := New(Options{Addr: "invalid-address"}).Start(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestShutdownTimeout(t *testing.T) {
	server := New(Options{Logger: logger.NewNop()})
	server.started.Store(true)
	server.wg.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	server.wg.Done()
}

func TestRegisterRejectsWhenFull(t *testing.T) {
	server := New(Options{Logger: logger.NewNop(), MaxClients: 1})
	server.online.Store(1)
	if _, _, err := server.register(&ConnectRequest{ClientID: "full"}, nil); !errors.Is(err, ErrTooManyClients) {
		t.Fatalf("register = %v, want ErrTooManyClients", err)
	}
}

func TestAuthenticatePanicIsRejected(t *testing.T) {
	server := New(Options{
		Addr:       "127.0.0.1:0",
		Logger:     logger.NewNop(),
		Auth:       AuthFunc(func(context.Context, ConnectRequest) error { panic("auth boom") }),
		Hooks:      Hooks{},
		MaxClients: 10,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	client := paho.NewClient(paho.NewClientOptions().
		AddBroker("tcp://" + server.Addr()).
		SetClientID("auth-panic").
		SetConnectTimeout(500 * time.Millisecond).
		SetAutoReconnect(false))
	token := client.Connect()
	if !token.WaitTimeout(2 * time.Second) {
		t.Fatal("connect timeout")
	}
	if token.Error() == nil {
		t.Fatal("expected authentication panic to reject connection")
	}
}

func TestListenLoopBackoffUntilCanceled(t *testing.T) {
	server := New(Options{Logger: logger.NewNop()})
	ctx, cancel := context.WithCancel(context.Background())
	server.ctx = ctx
	listener := &failingListener{closed: make(chan struct{})}
	server.wg.Add(1)
	done := make(chan struct{})
	go func() {
		server.acceptLoop(listener)
		close(done)
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not stop")
	}
	if listener.calls.Load() < 2 {
		t.Fatalf("accept calls = %d, want >= 2", listener.calls.Load())
	}
}

func TestHandleConnRecoversPanic(t *testing.T) {
	server := New(Options{Logger: logger.NewNop()})
	server.wg.Add(1)
	server.handleConn(&panicConn{})
}

func TestTLSListener(t *testing.T) {
	certPEM, keyPEM := selfSignedCertificate(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}

	server := New(Options{
		Addr:           "127.0.0.1:0",
		Logger:         logger.NewNop(),
		AllowAnonymous: true,
		TLSConfig:      &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	conn, err := tls.Dial("tcp", server.Addr(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if _, err := conn.Write(buildTestConnect("tls-client", true, 30, "", nil)); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	p, err := readPacket(reader, 1<<20)
	if err != nil {
		t.Fatalf("read connack: %v", err)
	}
	if p.typ != packetConnack || p.payload[1] != 0 {
		t.Fatalf("unexpected connack: %+v", p)
	}
}

func TestBrokerBootLifecycle(t *testing.T) {
	_ = os.Setenv("GOFRAME_BROKER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_BROKER_ADDR", "127.0.0.1:0")
	_ = os.Setenv("GOFRAME_BROKER_ALLOW_ANONYMOUS", "true")
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
	SetHooks(Hooks{})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	server := Get()
	if server == nil {
		t.Fatal("expected global broker")
	}
	client := connectPaho(t, server.Addr(), "boot-client", true, nil)
	if !client.IsConnected() {
		t.Fatal("expected broker to accept connections")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil broker after Shutdown")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	SetAuthenticator(nil)
	SetHooks(Hooks{})
}

func TestBrokerBootDisabled(t *testing.T) {
	_ = os.Setenv("GOFRAME_BROKER_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("GOFRAME_BROKER_ENABLED")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled broker must not be created")
	}
}

func TestBrokerBootTLSAndInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := selfSignedCertificate(t)
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	_ = os.Setenv("GOFRAME_BROKER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_BROKER_ADDR", "127.0.0.1:0")
	_ = os.Setenv("GOFRAME_BROKER_ALLOW_ANONYMOUS", "true")
	_ = os.Setenv("GOFRAME_BROKER_TLS_ENABLED", "true")
	_ = os.Setenv("GOFRAME_BROKER_TLS_CERT_FILE", certFile)
	_ = os.Setenv("GOFRAME_BROKER_TLS_KEY_FILE", keyFile)
	defer func() {
		for _, key := range []string{
			"GOFRAME_BROKER_ENABLED", "GOFRAME_BROKER_ADDR", "GOFRAME_BROKER_ALLOW_ANONYMOUS",
			"GOFRAME_BROKER_TLS_ENABLED", "GOFRAME_BROKER_TLS_CERT_FILE", "GOFRAME_BROKER_TLS_KEY_FILE",
		} {
			_ = os.Unsetenv(key)
		}
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup with TLS: %v", err)
	}
	server := Get()
	if server == nil || server.opts.TLSConfig == nil {
		t.Fatal("expected TLS broker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// 证书缺失时 Startup 必须失败。
	_ = os.Setenv("GOFRAME_BROKER_TLS_CERT_FILE", filepath.Join(dir, "missing.crt"))
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected TLS certificate error")
	}

	// 监听地址非法时 Startup 必须失败。
	_ = os.Setenv("GOFRAME_BROKER_TLS_ENABLED", "false")
	_ = os.Setenv("GOFRAME_BROKER_ADDR", "invalid-address")
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}

func selfSignedCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

type failingListener struct {
	calls  atomic.Int32
	closed chan struct{}
}

func (l *failingListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	return nil, errors.New("accept failed")
}

func (l *failingListener) Close() error   { return nil }
func (l *failingListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

type panicConn struct{}

func (c *panicConn) Read([]byte) (int, error)         { panic("read boom") }
func (c *panicConn) Write([]byte) (int, error)        { return 0, nil }
func (c *panicConn) Close() error                     { return nil }
func (c *panicConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *panicConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *panicConn) SetDeadline(time.Time) error      { return nil }
func (c *panicConn) SetReadDeadline(time.Time) error  { panic("deadline boom") }
func (c *panicConn) SetWriteDeadline(time.Time) error { return nil }
