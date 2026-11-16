package broker

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/god-jason/goframe/logger"
)

func newTestBroker(t *testing.T, opts Options) *Server {
	t.Helper()
	opts.Addr = "127.0.0.1:0"
	opts.Logger = logger.NewNop()
	if opts.Auth == nil {
		opts.AllowAnonymous = true
	}
	server := New(opts)
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return server
}

func connectPaho(t *testing.T, addr, clientID string, clean bool, handler paho.MessageHandler) paho.Client {
	t.Helper()
	opts := paho.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(clientID).
		SetCleanSession(clean).
		SetAutoReconnect(false).
		SetConnectTimeout(2 * time.Second)
	if handler != nil {
		opts.SetDefaultPublishHandler(handler)
	}
	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(3 * time.Second) {
		t.Fatal("mqtt connect timeout")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("mqtt connect: %v", err)
	}
	t.Cleanup(func() { client.Disconnect(100) })
	return client
}

func waitToken(t *testing.T, token paho.Token) {
	t.Helper()
	if !token.WaitTimeout(3 * time.Second) {
		t.Fatal("mqtt token timeout")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("mqtt token: %v", err)
	}
}

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", description)
}

func TestPublishSubscribe(t *testing.T) {
	server := newTestBroker(t, Options{})
	subscriber := connectPaho(t, server.Addr(), "subscriber", true, nil)
	publisher := connectPaho(t, server.Addr(), "publisher", true, nil)

	received := make(chan string, 1)
	waitToken(t, subscriber.Subscribe("devices/+/up", 1, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	}))

	waitToken(t, publisher.Publish("devices/1/up", 1, false, []byte("online")))

	select {
	case payload := <-received:
		if payload != "online" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message not received")
	}

	stats := server.Stats()
	if stats.Online != 2 || stats.MessagesIn == 0 || stats.MessagesOut == 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestRetainedMessage(t *testing.T) {
	server := newTestBroker(t, Options{})
	publisher := connectPaho(t, server.Addr(), "retained-pub", true, nil)
	waitToken(t, publisher.Publish("retained/topic", 1, true, []byte("value")))

	type result struct {
		payload  string
		retained bool
	}
	received := make(chan result, 1)
	subscriber := connectPaho(t, server.Addr(), "retained-sub", true, nil)
	waitToken(t, subscriber.Subscribe("retained/#", 1, func(_ paho.Client, message paho.Message) {
		received <- result{payload: string(message.Payload()), retained: message.Retained()}
	}))

	select {
	case got := <-received:
		if got.payload != "value" || !got.retained {
			t.Fatalf("unexpected retained message: %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retained message not received")
	}
	if server.Stats().Retained != 1 {
		t.Fatalf("retained = %d", server.Stats().Retained)
	}
}

func TestAuthentication(t *testing.T) {
	server := newTestBroker(t, Options{
		Auth: AuthFunc(func(_ context.Context, req ConnectRequest) error {
			if req.Username != "user" || string(req.Password) != "pass" {
				return errors.New("invalid credentials")
			}
			return nil
		}),
	})

	client := paho.NewClient(paho.NewClientOptions().
		AddBroker("tcp://" + server.Addr()).
		SetClientID("auth-fail").
		SetUsername("bad").
		SetPassword("bad").
		SetConnectTimeout(2 * time.Second))
	token := client.Connect()
	if !token.WaitTimeout(3 * time.Second) {
		t.Fatal("connect timeout")
	}
	if token.Error() == nil {
		t.Fatal("expected authentication failure")
	}

	okClient := paho.NewClient(paho.NewClientOptions().
		AddBroker("tcp://" + server.Addr()).
		SetClientID("auth-ok").
		SetUsername("user").
		SetPassword("pass").
		SetConnectTimeout(2 * time.Second))
	okToken := okClient.Connect()
	if !okToken.WaitTimeout(3*time.Second) || okToken.Error() != nil {
		t.Fatalf("authenticated connect failed: %v", okToken.Error())
	}
	t.Cleanup(func() { okClient.Disconnect(100) })
	if server.Stats().Online != 1 {
		t.Fatalf("online = %d", server.Stats().Online)
	}
}

func TestWillMessage(t *testing.T) {
	server := newTestBroker(t, Options{})
	subscriber := connectPaho(t, server.Addr(), "will-sub", true, nil)
	received := make(chan string, 1)
	waitToken(t, subscriber.Subscribe("will/topic", 0, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	}))

	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	connect := buildTestConnect("will-client", true, 30, "will/topic", []byte("gone"))
	if _, err := conn.Write(connect); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	ack := make([]byte, 4)
	if _, err := conn.Read(ack); err != nil || ack[0]>>4 != packetConnack {
		t.Fatalf("read connack: %v %v", ack, err)
	}
	_ = conn.Close()

	select {
	case payload := <-received:
		if payload != "gone" {
			t.Fatalf("will payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("will message not received")
	}
}

func TestPersistentSessionOfflineQueue(t *testing.T) {
	server := newTestBroker(t, Options{})

	// 持久会话订阅后正常断开。
	a1 := connectPaho(t, server.Addr(), "persistent", false, nil)
	waitToken(t, a1.Subscribe("queue/#", 1, nil))
	a1.Disconnect(100)
	waitFor(t, "first client offline", func() bool { return server.Stats().Online == 0 })

	// 另一个客户端发布 QoS 1 消息，Broker 应放入离线队列。
	b := connectPaho(t, server.Addr(), "queue-pub", true, nil)
	waitToken(t, b.Publish("queue/1", 1, false, []byte("queued")))

	// 同一 ClientID 重新连接，应收到离线消息。
	received := make(chan string, 1)
	connectPaho(t, server.Addr(), "persistent", false, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	select {
	case payload := <-received:
		if payload != "queued" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("offline message not delivered")
	}
}

func TestShutdown(t *testing.T) {
	server := newTestBroker(t, Options{})
	connectPaho(t, server.Addr(), "shutdown-client", true, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if server.Stats().Online != 0 {
		t.Fatalf("online = %d", server.Stats().Online)
	}
	if err := server.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after shutdown = %v, want ErrClosed", err)
	}
}

// buildTestConnect 仅用于测试：构造带 Will 的 CONNECT 报文。
func buildTestConnect(clientID string, clean bool, keepAlive uint16, willTopic string, willPayload []byte) []byte {
	payload := make([]byte, 0, 64)
	appendString := func(value string) {
		payload = append(payload, byte(len(value)>>8), byte(len(value)))
		payload = append(payload, value...)
	}
	appendString("MQTT")
	payload = append(payload, protocolLevel311)
	flags := byte(0)
	if clean {
		flags |= 0x02
	}
	if willTopic != "" {
		flags |= 0x04
	}
	payload = append(payload, flags)
	payload = append(payload, byte(keepAlive>>8), byte(keepAlive))
	appendString(clientID)
	if willTopic != "" {
		appendString(willTopic)
		payload = append(payload, byte(len(willPayload)>>8), byte(len(willPayload)))
		payload = append(payload, willPayload...)
	}
	return encodePacket(&packet{typ: packetConnect, payload: payload})
}
