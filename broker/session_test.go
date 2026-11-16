package broker

import (
	"bufio"
	"bytes"
	"testing"
	"time"

	"github.com/god-jason/goframe/logger"
)

func newTestSession(t *testing.T, clean bool) *Session {
	t.Helper()
	server := New(Options{Logger: logger.NewNop()})
	return newSession(1, server, &ConnectRequest{ClientID: "test", CleanSession: clean}, nil)
}

func dequeuePacket(t *testing.T, session *Session) *packet {
	t.Helper()
	select {
	case data := <-session.send:
		p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
		if err != nil {
			t.Fatalf("read queued packet: %v", err)
		}
		return p
	case <-time.After(time.Second):
		t.Fatal("no packet queued")
		return nil
	}
}

func TestSessionRetransmitsUnackedQoS1(t *testing.T) {
	session := newTestSession(t, false)
	session.mu.Lock()
	session.outbound[7] = outboundState{
		qos:     1,
		message: Message{Topic: "devices/1/down", Payload: []byte("cmd"), QoS: 1},
		sentAt:  time.Now().Add(-time.Hour),
	}
	session.mu.Unlock()

	session.retransmit(time.Second, time.Now())
	p := dequeuePacket(t, session)
	if p.typ != packetPublish {
		t.Fatalf("packet type = %d, want publish", p.typ)
	}
	message, err := parsePublish(p)
	if err != nil {
		t.Fatalf("parse publish: %v", err)
	}
	if message.PacketID != 7 || !message.Dup || message.QoS != 1 {
		t.Fatalf("unexpected retransmit: %+v", message)
	}
}

func TestSessionReplaysOutboundOnReconnect(t *testing.T) {
	old := newTestSession(t, false)
	old.mu.Lock()
	old.outbound[9] = outboundState{
		qos:        2,
		message:    Message{Topic: "devices/1/down", Payload: []byte("cmd"), QoS: 2},
		pubrelSent: true,
		sentAt:     time.Now().Add(-time.Hour),
	}
	old.mu.Unlock()

	session := newTestSession(t, false)
	session.copyStateFrom(old)
	if _, ok := session.outbound[9]; !ok {
		t.Fatal("expected outbound state to be inherited by persistent session")
	}

	session.replayOutbound()
	p := dequeuePacket(t, session)
	if p.typ != packetPubrel {
		t.Fatalf("packet type = %d, want pubrel", p.typ)
	}
	id, err := parsePacketID(p)
	if err != nil {
		t.Fatalf("parse packet id: %v", err)
	}
	if id != 9 {
		t.Fatalf("packet id = %d, want 9", id)
	}
}

func TestSessionRetransmitIgnoresFreshMessages(t *testing.T) {
	session := newTestSession(t, false)
	session.mu.Lock()
	session.outbound[3] = outboundState{
		qos:     1,
		message: Message{Topic: "t", Payload: []byte("x"), QoS: 1},
		sentAt:  time.Now(),
	}
	session.mu.Unlock()

	session.retransmit(time.Minute, time.Now())
	select {
	case <-session.send:
		t.Fatal("fresh message should not be retransmitted")
	default:
	}
}
