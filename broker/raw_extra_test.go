package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/god-jason/goframe/logger"
)

type connectSpec struct {
	clientID     string
	clean        bool
	keepAlive    uint16
	username     string
	password     string
	passwordOnly bool
	willTopic    string
	willPayload  []byte
	willQoS      byte
	willRetain   bool
	level        byte
	protocol     string
	flags        byte
	forceFlags   bool
}

func (s connectSpec) packet() *packet {
	if s.protocol == "" {
		s.protocol = "MQTT"
	}
	if s.level == 0 {
		s.level = protocolLevel311
	}
	var buf bytes.Buffer
	writeString(&buf, s.protocol)
	buf.WriteByte(s.level)

	flags := byte(0)
	if s.clean {
		flags |= 0x02
	}
	if s.willTopic != "" {
		flags |= 0x04 | s.willQoS<<3
		if s.willRetain {
			flags |= 0x20
		}
	}
	if s.username != "" {
		flags |= 0x80
	}
	if s.password != "" || s.passwordOnly {
		flags |= 0x40
	}
	if s.forceFlags {
		flags = s.flags
	}
	buf.WriteByte(flags)
	_ = binary.Write(&buf, binary.BigEndian, s.keepAlive)
	writeString(&buf, s.clientID)
	if s.willTopic != "" {
		writeString(&buf, s.willTopic)
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.willPayload)))
		buf.Write(s.willPayload)
	}
	if s.username != "" {
		writeString(&buf, s.username)
	}
	if s.password != "" || s.passwordOnly {
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(s.password)))
		buf.WriteString(s.password)
	}
	return &packet{typ: packetConnect, payload: buf.Bytes()}
}

func dialRaw(t *testing.T, addr string, spec connectSpec) (net.Conn, *bufio.Reader, byte, bool) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write(encodePacket(spec.packet())); err != nil {
		conn.Close()
		t.Fatalf("write connect: %v", err)
	}
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	ack, err := readPacket(reader, 1<<20)
	if err != nil {
		conn.Close()
		t.Fatalf("read connack: %v", err)
	}
	if ack.typ != packetConnack || len(ack.payload) < 2 {
		conn.Close()
		t.Fatalf("unexpected connack: %+v", ack)
	}
	present := ack.payload[0]&0x01 != 0
	return conn, reader, ack.payload[1], present
}

func readRawPacket(t *testing.T, conn net.Conn, reader *bufio.Reader) *packet {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	p, err := readPacket(reader, 1<<20)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	return p
}

func expectClosed(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := readPacket(reader, 1<<20); err == nil {
		t.Fatal("expected connection to be closed")
	}
}

func buildSubscribePacket(id uint16, subscriptions ...Subscription) *packet {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, id)
	for _, subscription := range subscriptions {
		writeString(&buf, subscription.Filter)
		buf.WriteByte(subscription.QoS)
	}
	return &packet{typ: packetSubscribe, flags: 0x02, payload: buf.Bytes()}
}

func buildUnsubscribePacket(id uint16, filters ...string) *packet {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, id)
	for _, filter := range filters {
		writeString(&buf, filter)
	}
	return &packet{typ: packetUnsubscribe, flags: 0x02, payload: buf.Bytes()}
}

func TestRawProtocolHandshake(t *testing.T) {
	server := newTestBroker(t, Options{})
	received := make(chan string, 4)
	subscriber := connectPaho(t, server.Addr(), "raw-dedup-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("raw/#", 2, nil))

	conn, reader, code, present := dialRaw(t, server.Addr(), connectSpec{clientID: "raw-client", clean: true, keepAlive: 30})
	defer conn.Close()
	if code != 0 || present {
		t.Fatalf("connack code=%d present=%v", code, present)
	}

	// 重复的 QoS2 报文只投递一次。
	for i := 0; i < 2; i++ {
		if _, err := conn.Write(encodePacket(buildPublish("raw/dup", []byte("dup"), 2, false, false, 7))); err != nil {
			t.Fatalf("write publish: %v", err)
		}
		ack := readRawPacket(t, conn, reader)
		if ack.typ != packetPubrec {
			t.Fatalf("ack type = %d, want pubrec", ack.typ)
		}
	}
	select {
	case payload := <-received:
		if payload != "dup" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("qos2 message not routed")
	}
	select {
	case payload := <-received:
		t.Fatalf("duplicate qos2 message routed: %q", payload)
	case <-time.After(150 * time.Millisecond):
	}

	if _, err := conn.Write(encodePacket(buildAck(packetPubrel, 7))); err != nil {
		t.Fatalf("write pubrel: %v", err)
	}
	if ack := readRawPacket(t, conn, reader); ack.typ != packetPubcomp {
		t.Fatalf("ack type = %d, want pubcomp", ack.typ)
	}

	if _, err := conn.Write(encodePacket(buildPublish("raw/qos1", []byte("one"), 1, false, false, 8))); err != nil {
		t.Fatalf("write qos1: %v", err)
	}
	if ack := readRawPacket(t, conn, reader); ack.typ != packetPuback {
		t.Fatalf("ack type = %d, want puback", ack.typ)
	}

	if _, err := conn.Write(encodePacket(&packet{typ: packetPingreq})); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	if p := readRawPacket(t, conn, reader); p.typ != packetPingresp {
		t.Fatalf("packet type = %d, want pingresp", p.typ)
	}

	if _, err := conn.Write(encodePacket(buildSubscribePacket(9, Subscription{Filter: "raw/add", QoS: 1}))); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	if p := readRawPacket(t, conn, reader); p.typ != packetSuback || len(p.payload) != 3 || p.payload[2] != 1 {
		t.Fatalf("unexpected suback: %+v", p)
	}

	if _, err := conn.Write(encodePacket(buildUnsubscribePacket(10, "raw/add", "never/subscribed"))); err != nil {
		t.Fatalf("write unsubscribe: %v", err)
	}
	if p := readRawPacket(t, conn, reader); p.typ != packetUnsuback {
		t.Fatalf("packet type = %d, want unsuback", p.typ)
	}

	if _, err := conn.Write(encodePacket(&packet{typ: packetDisconnect})); err != nil {
		t.Fatalf("write disconnect: %v", err)
	}
	expectClosed(t, conn, reader)
}

func TestRawConnackFailures(t *testing.T) {
	t.Run("unsupported protocol", func(t *testing.T) {
		server := newTestBroker(t, Options{})
		conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "v5", clean: true, level: 5})
		defer conn.Close()
		if code != 0x01 {
			t.Fatalf("connack code = %d, want 1", code)
		}
		expectClosed(t, conn, reader)
	})

	t.Run("anonymous rejected", func(t *testing.T) {
		server := New(Options{Addr: "127.0.0.1:0", Logger: logger.NewNop(), AllowAnonymous: false})
		if err := server.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
		conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "anon", clean: true})
		defer conn.Close()
		if code != 0x05 {
			t.Fatalf("connack code = %d, want 5", code)
		}
		expectClosed(t, conn, reader)
	})

	t.Run("max clients", func(t *testing.T) {
		server := New(Options{Addr: "127.0.0.1:0", Logger: logger.NewNop(), AllowAnonymous: true, MaxClients: 1})
		if err := server.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
		first, firstReader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "first", clean: true})
		defer first.Close()
		if code != 0 {
			t.Fatalf("first connack code = %d", code)
		}
		_ = firstReader

		second, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("dial second: %v", err)
		}
		defer second.Close()
		_, _ = second.Write(encodePacket(connectSpec{clientID: "second", clean: true}.packet()))
		expectClosed(t, second, bufio.NewReader(second))
	})
}

func TestRawMalformedConnections(t *testing.T) {
	server := newTestBroker(t, Options{MaxPacketSize: 64})

	t.Run("first packet not connect", func(t *testing.T) {
		conn, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		_, _ = conn.Write(encodePacket(&packet{typ: packetPingreq}))
		expectClosed(t, conn, bufio.NewReader(conn))
	})

	cases := map[string]connectSpec{
		"reserved flag":         {clientID: "bad-flags", clean: true, forceFlags: true, flags: 0x01},
		"will qos without will": {clientID: "bad-will", clean: true, forceFlags: true, flags: 0x08},
		"empty client id":       {clientID: "", clean: false},
		"invalid will topic":    {clientID: "bad-will-topic", clean: true, willTopic: "a/#", willPayload: []byte("x")},
		"packet too large":      {clientID: string(bytes.Repeat([]byte("x"), 80)), clean: true},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", server.Addr())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			_, _ = conn.Write(encodePacket(spec.packet()))
			expectClosed(t, conn, bufio.NewReader(conn))
		})
	}
}

func TestRawInvalidPacketsCloseConnection(t *testing.T) {
	server := newTestBroker(t, Options{})
	cases := map[string]*packet{
		"publish wildcard":     buildPublish("bad/+", []byte("x"), 0, false, false, 0),
		"publish zero id":      buildPublish("bad/id", []byte("x"), 1, false, false, 0),
		"subscribe bad flag":   {typ: packetSubscribe, flags: 0x00, payload: []byte{0, 1, 0, 1, 'a', 0}},
		"subscribe bad filter": buildSubscribePacket(1, Subscription{Filter: "a/#/b", QoS: 0}),
		"unsubscribe empty":    {typ: packetUnsubscribe, flags: 0x02, payload: []byte{0, 1}},
		"puback short":         {typ: packetPuback, payload: []byte{0}},
		"unknown type":         {typ: 15, payload: []byte{0}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "invalid-" + name, clean: true})
			defer conn.Close()
			if code != 0 {
				t.Fatalf("connack code = %d", code)
			}
			_, _ = conn.Write(encodePacket(p))
			expectClosed(t, conn, reader)
		})
	}
}

func TestRawSessionTakeover(t *testing.T) {
	server := newTestBroker(t, Options{})

	first, firstReader, _, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "same", clean: false})
	defer first.Close()
	_, _ = first.Write(encodePacket(buildSubscribePacket(1, Subscription{Filter: "takeover/#", QoS: 1})))
	if p := readRawPacket(t, first, firstReader); p.typ != packetSuback {
		t.Fatalf("packet type = %d, want suback", p.typ)
	}

	second, secondReader, code, present := dialRaw(t, server.Addr(), connectSpec{clientID: "same", clean: false})
	defer second.Close()
	if code != 0 || !present {
		t.Fatalf("connack code=%d present=%v", code, present)
	}
	expectClosed(t, first, firstReader)
	waitFor(t, "single online client", func() bool { return server.Stats().Online == 1 })

	third, _, code, present := dialRaw(t, server.Addr(), connectSpec{clientID: "same", clean: true})
	defer third.Close()
	if code != 0 || present {
		t.Fatalf("clean takeover connack code=%d present=%v", code, present)
	}
	expectClosed(t, second, secondReader)
	waitFor(t, "clean session replaced", func() bool {
		infos := server.Sessions()
		return len(infos) == 1 && infos[0].CleanSession && infos[0].Subscriptions == 0
	})
}

func TestRawWillOnKeepAliveTimeout(t *testing.T) {
	server := newTestBroker(t, Options{})
	received := make(chan string, 1)
	subscriber := connectPaho(t, server.Addr(), "will-timeout-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("will/timeout", 0, nil))

	conn, _, code, _ := dialRaw(t, server.Addr(), connectSpec{
		clientID:    "will-timeout",
		clean:       true,
		keepAlive:   1,
		willTopic:   "will/timeout",
		willPayload: []byte("timed-out"),
	})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	select {
	case payload := <-received:
		if payload != "timed-out" {
			t.Fatalf("will payload = %q", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("will message not delivered after keepalive timeout")
	}
}

func TestRawWillSuppressedOnDisconnect(t *testing.T) {
	server := newTestBroker(t, Options{})
	received := make(chan string, 1)
	subscriber := connectPaho(t, server.Addr(), "will-suppress-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("will/suppress", 0, nil))

	conn, _, code, _ := dialRaw(t, server.Addr(), connectSpec{
		clientID:    "will-suppress",
		clean:       true,
		willTopic:   "will/suppress",
		willPayload: []byte("should-not-arrive"),
	})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	if _, err := conn.Write(encodePacket(&packet{typ: packetDisconnect})); err != nil {
		t.Fatalf("write disconnect: %v", err)
	}
	select {
	case payload := <-received:
		t.Fatalf("unexpected will message: %q", payload)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRawWillRetained(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, _, code, _ := dialRaw(t, server.Addr(), connectSpec{
		clientID:    "will-retain",
		clean:       true,
		willTopic:   "will/retained",
		willPayload: []byte("retained"),
		willQoS:     1,
		willRetain:  true,
	})
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	_ = conn.Close()
	waitFor(t, "will retained", func() bool { return server.Stats().Retained == 1 })

	received := make(chan string, 1)
	subscriber := connectPaho(t, server.Addr(), "will-retained-sub", true, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})
	waitToken(t, subscriber.Subscribe("will/retained", 1, nil))
	select {
	case payload := <-received:
		if payload != "retained" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retained will not delivered")
	}
}

func TestRawMaxSubscriptions(t *testing.T) {
	server := newTestBroker(t, Options{MaxSubscriptions: 1})
	conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "sub-limit", clean: true})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	_, _ = conn.Write(encodePacket(buildSubscribePacket(1,
		Subscription{Filter: "one", QoS: 0},
		Subscription{Filter: "two", QoS: 1},
	)))
	p := readRawPacket(t, conn, reader)
	if p.typ != packetSuback || len(p.payload) != 4 || p.payload[2] != 0 || p.payload[3] != 0x80 {
		t.Fatalf("unexpected suback: %+v", p.payload)
	}
}

func TestRawKeepAliveZeroDisablesDeadline(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, reader, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "keepalive-zero", clean: true, keepAlive: 0})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := conn.Write(encodePacket(&packet{typ: packetPingreq})); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	if p := readRawPacket(t, conn, reader); p.typ != packetPingresp {
		t.Fatalf("packet type = %d", p.typ)
	}
}

func TestRawWrongProtocolName(t *testing.T) {
	server := newTestBroker(t, Options{})
	cases := []connectSpec{
		{clientID: "wrong-311", clean: true, protocol: "MQIsdp", level: protocolLevel311},
		{clientID: "wrong-31", clean: true, protocol: "MQTT", level: protocolLevel31},
	}
	for _, spec := range cases {
		conn, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_, _ = conn.Write(encodePacket(spec.packet()))
		expectClosed(t, conn, bufio.NewReader(conn))
		conn.Close()
	}
}

func TestRawMQTT31Connect(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, _, code, present := dialRaw(t, server.Addr(), connectSpec{
		clientID: "mqtt31", clean: false, protocol: "MQIsdp", level: protocolLevel31,
	})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	// MQTT 3.1 不允许设置 Session Present。
	if present {
		t.Fatal("mqtt 3.1 connack must not set session present")
	}
}

func TestRawUsernamePassword(t *testing.T) {
	server := newTestBroker(t, Options{Auth: AuthFunc(func(_ context.Context, req ConnectRequest) error {
		if req.Username != "user" || string(req.Password) != "secret" {
			return errors.New("invalid credentials")
		}
		return nil
	})})
	conn, _, code, _ := dialRaw(t, server.Addr(), connectSpec{
		clientID: "creds", clean: true, username: "user", password: "secret",
	})
	defer conn.Close()
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}

	// 密码不带用户名保持兼容，但鉴权器仍会拒绝。
	conn2, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn2.Close()
	_, _ = conn2.Write(encodePacket(connectSpec{clientID: "password-only", clean: true, passwordOnly: true, password: "secret"}.packet()))
	reader := bufio.NewReader(conn2)
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	ack, err := readPacket(reader, 1<<20)
	if err != nil {
		t.Fatalf("read connack: %v", err)
	}
	if ack.typ != packetConnack || ack.payload[1] != 0x05 {
		t.Fatalf("unexpected connack: %+v", ack.payload)
	}
}

func TestRawReconnectPendingReplay(t *testing.T) {
	server := newTestBroker(t, Options{})
	conn, _, code, _ := dialRaw(t, server.Addr(), connectSpec{clientID: "replay", clean: false})
	if code != 0 {
		t.Fatalf("connack code = %d", code)
	}
	_, _ = conn.Write(encodePacket(buildSubscribePacket(1, Subscription{Filter: "replay/#", QoS: 1})))
	_ = conn.Close()
	waitFor(t, "offline", func() bool { return server.Stats().Online == 0 })

	publisher := connectPaho(t, server.Addr(), "replay-pub", true, nil)
	waitToken(t, publisher.Publish("replay/topic", 1, false, []byte("pending")))

	second, reader, code, present := dialRaw(t, server.Addr(), connectSpec{clientID: "replay", clean: false})
	defer second.Close()
	if code != 0 || !present {
		t.Fatalf("connack code=%d present=%v", code, present)
	}
	p := readRawPacket(t, second, reader)
	if p.typ != packetPublish {
		t.Fatalf("packet type = %d, want publish", p.typ)
	}
	message, err := parsePublish(p)
	if err != nil {
		t.Fatalf("parse publish: %v", err)
	}
	if message.Topic != "replay/topic" || string(message.Payload) != "pending" {
		t.Fatalf("unexpected replay message: %+v", message)
	}
	_, _ = second.Write(encodePacket(buildAck(packetPuback, message.PacketID)))
}
