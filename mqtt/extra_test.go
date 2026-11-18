package mqtt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/broker"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

func startLocalBroker(t *testing.T) *broker.Server {
	t.Helper()
	server := broker.New(broker.Options{Addr: "127.0.0.1:0", Logger: logger.NewNop(), AllowAnonymous: true})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return server
}

func newConnectedClient(t *testing.T, addr string, opts Options) *Client {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	if len(opts.Brokers) == 0 {
		opts.Brokers = []string{"tcp://" + addr}
	}
	if opts.ClientID == "" {
		opts.ClientID = "mqtt-test"
	}
	client, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	})
	return client
}

func TestConnectedPublishSubscribeFlow(t *testing.T) {
	server := startLocalBroker(t)
	client := newConnectedClient(t, server.Addr(), Options{
		Username:      "user",
		Password:      "pass",
		CleanSession:  true,
		AutoReconnect: false,
		KeepAlive:     time.Second,
	})
	if !client.IsConnected() || !client.Stats().Connected {
		t.Fatal("expected connected client")
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	received := make(chan []byte, 4)
	if err := client.Subscribe(context.Background(), "mqtt/#", 1, func(_ string, payload []byte) {
		received <- append([]byte(nil), payload...)
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := client.Publish(context.Background(), "mqtt/topic", 1, false, []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case payload := <-received:
		if string(payload) != "hello" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message not received")
	}

	if err := client.PublishJSON(context.Background(), "mqtt/json", 0, false, map[string]any{"a": 1}); err != nil {
		t.Fatalf("PublishJSON: %v", err)
	}
	select {
	case payload := <-received:
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded["a"] != float64(1) {
			t.Fatalf("json payload = %q, %v", payload, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("json message not received")
	}

	if err := client.PublishJSON(context.Background(), "mqtt/bad", 0, false, make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}

	if err := client.Unsubscribe(context.Background(), "mqtt/#"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err := client.Unsubscribe(context.Background()); err != nil {
		t.Fatalf("Unsubscribe with no topics: %v", err)
	}
	if err := client.Unsubscribe(context.Background(), "mqtt/#/bad"); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("invalid unsubscribe = %v, want ErrInvalidTopic", err)
	}
	if err := client.Publish(context.Background(), "mqtt/#", 0, false, nil); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("invalid publish = %v, want ErrInvalidTopic", err)
	}

	if err := client.Publish(context.Background(), "mqtt/after", 0, false, []byte("x")); err != nil {
		t.Fatalf("Publish after unsubscribe: %v", err)
	}
	select {
	case payload := <-received:
		t.Fatalf("unexpected message after unsubscribe: %q", payload)
	case <-time.After(200 * time.Millisecond):
	}

	// 处理器 panic 不应影响后续消息。
	if err := client.Subscribe(context.Background(), "mqtt/panic", 0, func(string, []byte) {
		panic("handler boom")
	}); err != nil {
		t.Fatalf("Subscribe panic handler: %v", err)
	}
	if err := client.Publish(context.Background(), "mqtt/panic", 0, false, []byte("boom")); err != nil {
		t.Fatalf("Publish panic: %v", err)
	}
	if err := client.Subscribe(context.Background(), "mqtt/alive", 0, func(_ string, payload []byte) {
		received <- append([]byte(nil), payload...)
	}); err != nil {
		t.Fatalf("Subscribe alive: %v", err)
	}
	if err := client.Publish(context.Background(), "mqtt/alive", 0, false, []byte("alive")); err != nil {
		t.Fatalf("Publish alive: %v", err)
	}
	select {
	case payload := <-received:
		if string(payload) != "alive" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connection did not survive handler panic")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if client.IsConnected() {
		t.Fatal("client must be disconnected after Stop")
	}
	if err := client.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if err := client.Publish(context.Background(), "mqtt/topic", 0, false, []byte("x")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Publish after stop = %v, want ErrNotConnected", err)
	}

	canceled, cancelFunc := context.WithCancel(context.Background())
	cancelFunc()
	if err := client.Stop(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop with canceled ctx = %v", err)
	}
}

func TestStartFailuresAndContextCancel(t *testing.T) {
	client, err := New(Options{
		Logger:         logger.NewNop(),
		Brokers:        []string{"127.0.0.1:1"},
		ClientID:       "unreachable",
		ConnectTimeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := client.Start(ctx); err == nil {
		t.Fatal("expected connect failure")
	}
	if client.IsConnected() {
		t.Fatal("client must not be connected")
	}

	if err := waitToken(context.Background(), nil); err != nil {
		t.Fatalf("waitToken(nil) = %v", err)
	}

	// 端口拒绝连接时 token 会快速返回错误，覆盖 token.Error 分支。
	fastClient, err := New(Options{
		Logger:         logger.NewNop(),
		Brokers:        []string{"127.0.0.1:1"},
		ClientID:       "fast-fail",
		ConnectTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fastClient.Start(context.Background()); err == nil {
		t.Fatal("expected token error")
	}

	longTopic := strings.Repeat("a", 65536)
	if err := validatePublishTopic(longTopic); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("publish long topic = %v", err)
	}
	if err := validateSubscribeTopic(longTopic); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("subscribe long topic = %v", err)
	}
	if err := validateSubscribeTopic("a\x00b"); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("subscribe null topic = %v", err)
	}
}

func TestNewFillsOptionalSettings(t *testing.T) {
	client, err := New(Options{
		Brokers:      []string{"127.0.0.1:1883"},
		Username:     "user",
		Password:     "pass",
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
		KeepAlive:    0,
		ClientID:     "",
		CleanSession: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stats := client.Stats()
	if stats.ClientID == "" || len(stats.Brokers) != 1 || stats.Connected {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if client.log == nil {
		t.Fatal("expected default logger")
	}
}

func TestBuildTLSConfigWithValidFiles(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := testCertificate(t)
	caFile := filepath.Join(dir, "ca.pem")
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	for name, data := range map[string][]byte{caFile: certPEM, certFile: certPEM, keyFile: keyPEM} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cfg, err := BuildTLSConfig(caFile, certFile, keyFile, "mqtt.example.com")
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg.RootCAs == nil || len(cfg.Certificates) != 1 || cfg.ServerName != "mqtt.example.com" {
		t.Fatalf("unexpected tls config: %+v", cfg)
	}
	if cfg.MinVersion == 0 {
		t.Fatal("expected TLS min version")
	}
}

func TestAutoReconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	server := broker.New(broker.Options{Addr: addr, Logger: logger.NewNop(), AllowAnonymous: true})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	client, err := New(Options{
		Logger:            logger.NewNop(),
		Brokers:           []string{"tcp://" + addr},
		ClientID:          "reconnect-client",
		AutoReconnect:     true,
		ReconnectInterval: 50 * time.Millisecond,
		ConnectTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown broker: %v", err)
	}
	shutdownCancel()
	waitCondition(t, "connection lost", func() bool { return !client.IsConnected() })

	restarted := broker.New(broker.Options{Addr: addr, Logger: logger.NewNop(), AllowAnonymous: true})
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatalf("restart broker: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = restarted.Shutdown(ctx)
	}()
	waitCondition(t, "reconnected", func() bool { return client.IsConnected() })
}

func waitCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", description)
}

func TestMQTTBootLifecycle(t *testing.T) {
	server := startLocalBroker(t)
	_ = os.Setenv("GOFRAME_MQTT_ENABLED", "true")
	_ = os.Setenv("GOFRAME_MQTT_BROKERS", "tcp://"+server.Addr())
	_ = os.Setenv("GOFRAME_MQTT_CLIENT_ID", "boot-mqtt")
	_ = os.Setenv("GOFRAME_MQTT_KEEP_ALIVE", "1s")
	_ = os.Setenv("GOFRAME_MQTT_CONNECT_TIMEOUT", "2s")
	defer func() {
		for _, key := range []string{
			"GOFRAME_MQTT_ENABLED", "GOFRAME_MQTT_BROKERS", "GOFRAME_MQTT_CLIENT_ID",
			"GOFRAME_MQTT_KEEP_ALIVE", "GOFRAME_MQTT_CONNECT_TIMEOUT",
		} {
			_ = os.Unsetenv(key)
		}
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	client := Get()
	if client == nil || !client.IsConnected() {
		t.Fatal("expected connected global client")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Shutdown(stopCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil client after Shutdown")
	}
	if err := Shutdown(stopCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestMQTTBootDisabledAndInvalid(t *testing.T) {
	_ = os.Setenv("GOFRAME_MQTT_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("GOFRAME_MQTT_ENABLED")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup disabled: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled mqtt must not create client")
	}

	_ = os.Setenv("GOFRAME_MQTT_ENABLED", "true")
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected brokers validation error")
	}

	_ = os.Setenv("GOFRAME_MQTT_BROKERS", "tcp://127.0.0.1:1")
	_ = os.Setenv("GOFRAME_MQTT_CONNECT_TIMEOUT", "200ms")
	_ = os.Setenv("GOFRAME_MQTT_TLS_ENABLED", "true")
	_ = os.Setenv("GOFRAME_MQTT_TLS_CA_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	defer func() {
		_ = os.Unsetenv("GOFRAME_MQTT_BROKERS")
		_ = os.Unsetenv("GOFRAME_MQTT_CONNECT_TIMEOUT")
		_ = os.Unsetenv("GOFRAME_MQTT_TLS_ENABLED")
		_ = os.Unsetenv("GOFRAME_MQTT_TLS_CA_FILE")
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected TLS config error")
	}

	// TLS 关闭后连接不可达地址，Startup 应返回连接错误。
	_ = os.Setenv("GOFRAME_MQTT_TLS_ENABLED", "false")
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected connect error")
	}
}

func TestCertificate(t *testing.T) {
	certPEM, keyPEM := testCertificate(t)
	if block, _ := pem.Decode(certPEM); block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("invalid certificate PEM")
	}
	if block, _ := pem.Decode(keyPEM); block == nil {
		t.Fatal("invalid key PEM")
	}
}

func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mqtt.example.com"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{"mqtt.example.com"},
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
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
