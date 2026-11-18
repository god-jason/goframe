package broker

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/god-jason/goframe/logger"
)

func newPipeSession(t *testing.T, opts Options) (*Session, net.Conn, net.Conn) {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	server := New(opts)
	serverConn, peer := net.Pipe()
	session := newSession(1, server, &ConnectRequest{ClientID: "pipe", CleanSession: false, KeepAlive: 30}, serverConn)
	return session, serverConn, peer
}

func TestWriteLoopStopsOnWriteFailure(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	session.startWriter()
	_ = peer.Close()

	session.enqueue(buildPingresp())
	waitFor(t, "session closed after write failure", func() bool { return !session.Online() })
	session.close()
}

func TestRetransmitLoopSendsStaleMessages(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{RetransmitInterval: 10 * time.Millisecond})
	defer peer.Close()
	session.mu.Lock()
	session.outbound[1] = outboundState{
		qos:     1,
		message: Message{Topic: "retry/topic", Payload: []byte("retry"), QoS: 1},
		sentAt:  time.Now().Add(-time.Hour),
	}
	session.mu.Unlock()
	go session.retransmitLoop()
	defer session.close()

	select {
	case data := <-session.send:
		p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
		if err != nil {
			t.Fatalf("read packet: %v", err)
		}
		if p.typ != packetPublish {
			t.Fatalf("packet type = %d, want publish", p.typ)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retransmit loop did not send packet")
	}
}

func TestWriteRawErrors(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	_ = peer.Close()
	_ = session.conn.Close()
	if err := session.writeRaw([]byte("x")); err == nil {
		t.Fatal("expected write error on closed conn")
	}

	nilConn := newSession(2, New(Options{Logger: logger.NewNop()}), &ConnectRequest{ClientID: "nil"}, nil)
	if err := nilConn.writeRaw([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("writeRaw nil conn = %v, want net.ErrClosed", err)
	}
}

func TestEnqueueWaitAfterClose(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{SendQueue: 1})
	defer peer.Close()
	session.send <- []byte("buffered")
	session.close()
	if session.enqueueWait(buildPingresp()) {
		t.Fatal("enqueueWait must fail after close")
	}
	if session.enqueue(buildPingresp()) {
		t.Fatal("enqueue must fail after close")
	}
}

func TestEnqueueSlowConsumerClosesSession(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{SendQueue: 1})
	defer peer.Close()
	if !session.enqueue(buildPingresp()) {
		t.Fatal("first enqueue should succeed")
	}
	if session.enqueue(buildPingresp()) {
		t.Fatal("second enqueue should close slow consumer")
	}
	if session.Online() {
		t.Fatal("session must be offline")
	}
}

func TestDeliverOfflineSession(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	defer peer.Close()
	session.close()
	if session.deliver(&Message{Topic: "t"}, 1, false) {
		t.Fatal("offline session must reject delivery")
	}
}

func TestPacketIDExhaustionFallsBackToQoS0(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	defer peer.Close()
	session.mu.Lock()
	for i := 1; i < 65536; i++ {
		session.outbound[uint16(i)] = outboundState{qos: 1}
	}
	session.mu.Unlock()

	if id := session.nextPacketIDLocked(); id != 0 {
		t.Fatalf("nextPacketIDLocked = %d, want 0", id)
	}

	session.mu.Lock()
	session.nextID = 65535
	delete(session.outbound, 1)
	id := session.nextPacketIDLocked()
	session.mu.Unlock()
	if id != 1 {
		t.Fatalf("wrapped packet id = %d, want 1", id)
	}

	session.mu.Lock()
	for i := 1; i < 65536; i++ {
		session.outbound[uint16(i)] = outboundState{qos: 1}
	}
	session.mu.Unlock()
	if !session.deliver(&Message{Topic: "fallback", Payload: []byte("x")}, 1, false) {
		t.Fatal("expected fallback delivery to succeed")
	}
	select {
	case data := <-session.send:
		p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
		if err != nil {
			t.Fatalf("read packet: %v", err)
		}
		if message, err := parsePublish(p); err != nil || message.QoS != 0 {
			t.Fatalf("fallback message = %+v, %v", message, err)
		}
	default:
		t.Fatal("no fallback packet queued")
	}
}

func TestSessionSubscriptionEdges(t *testing.T) {
	server := New(Options{Logger: logger.NewNop(), MaxSubscriptions: 1})
	session := newSession(1, server, &ConnectRequest{ClientID: "subs", CleanSession: true}, nil)

	if !session.addSubscription("a", 0) {
		t.Fatal("first subscription should succeed")
	}
	if !session.addSubscription("a", 1) {
		t.Fatal("updating existing subscription should succeed")
	}
	if session.Subscriptions()["a"] != 1 {
		t.Fatalf("subscriptions = %+v", session.Subscriptions())
	}
	if session.addSubscription("b", 0) {
		t.Fatal("subscription limit should reject new filter")
	}
	session.removeSubscription("a")
	if len(session.Subscriptions()) != 0 {
		t.Fatalf("subscriptions = %+v", session.Subscriptions())
	}
	session.copyStateFrom(nil)
}

func TestInboundQoS2Dedup(t *testing.T) {
	session := newSession(1, New(Options{Logger: logger.NewNop()}), &ConnectRequest{ClientID: "qos2"}, nil)
	if !session.markInboundQoS2(5) {
		t.Fatal("first inbound qos2 should be accepted")
	}
	if session.markInboundQoS2(5) {
		t.Fatal("duplicate inbound qos2 must be rejected")
	}
	session.clearInboundQoS2(5)
	if !session.markInboundQoS2(5) {
		t.Fatal("cleared inbound qos2 should be accepted again")
	}
}

func TestHandlePubrecUnknownAndPubcomp(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	defer peer.Close()

	session.handlePubrec(99)
	select {
	case data := <-session.send:
		p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
		if err != nil {
			t.Fatalf("read packet: %v", err)
		}
		if p.typ != packetPubrel {
			t.Fatalf("packet type = %d, want pubrel", p.typ)
		}
	default:
		t.Fatal("expected pubrel packet")
	}

	session.mu.Lock()
	session.outbound[3] = outboundState{qos: 2}
	session.mu.Unlock()
	session.handlePubrec(3)
	session.mu.RLock()
	if state := session.outbound[3]; !state.pubrelSent {
		t.Fatalf("expected pubrelSent, got %+v", state)
	}
	session.mu.RUnlock()
	<-session.send

	session.handlePubcomp(3)
	session.handlePuback(3)
	session.mu.RLock()
	if len(session.outbound) != 0 {
		t.Fatalf("outbound = %+v", session.outbound)
	}
	session.mu.RUnlock()
}

func TestRetransmitBatchLimit(t *testing.T) {
	session, _, peer := newPipeSession(t, Options{})
	defer peer.Close()
	session.mu.Lock()
	for i := 1; i <= 70; i++ {
		session.outbound[uint16(i)] = outboundState{
			qos:     1,
			message: Message{Topic: "batch", Payload: []byte("x"), QoS: 1},
			sentAt:  time.Now().Add(-time.Hour),
		}
	}
	session.mu.Unlock()

	session.retransmit(time.Second, time.Now())
	if queued := len(session.send); queued != 64 {
		t.Fatalf("queued = %d, want 64", queued)
	}
}

func TestSessionCopyStateFromPersistent(t *testing.T) {
	old := newSession(1, New(Options{Logger: logger.NewNop()}), &ConnectRequest{ClientID: "old"}, nil)
	old.mu.Lock()
	old.subs["old/#"] = 1
	old.pending = append(old.pending, pendingMessage{message: Message{Topic: "p"}, qos: 1})
	old.outbound[4] = outboundState{qos: 1, message: Message{Topic: "o", Payload: []byte("p")}}
	old.inboundQoS2[7] = struct{}{}
	old.nextID = 42
	old.mu.Unlock()

	session := newSession(2, New(Options{Logger: logger.NewNop()}), &ConnectRequest{ClientID: "new"}, nil)
	session.copyStateFrom(old)
	if len(session.subs) != 1 || len(session.pending) != 1 || len(session.outbound) != 1 || len(session.inboundQoS2) != 1 {
		t.Fatalf("copied state: subs=%v pending=%v outbound=%v inbound=%v",
			session.subs, session.pending, session.outbound, session.inboundQoS2)
	}
	if session.nextID != 42 {
		t.Fatalf("nextID = %d, want 42", session.nextID)
	}
	if max(1, 2) != 2 || max(3, 2) != 3 {
		t.Fatal("unexpected max result")
	}
}
