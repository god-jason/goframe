package broker

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

func TestPacketRemainingLengthRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, 127, 128, 16383, 16384, 65535} {
		payload := bytes.Repeat([]byte("x"), size)
		data := encodePacket(&packet{typ: packetPublish, payload: payload})
		p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if p.typ != packetPublish || len(p.payload) != size {
			t.Fatalf("size %d: type=%d payload=%d", size, p.typ, len(p.payload))
		}
	}
}

func TestReadPacketTooLarge(t *testing.T) {
	data := encodePacket(&packet{typ: packetPublish, payload: bytes.Repeat([]byte("x"), 1024)})
	if _, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 100); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("err = %v, want ErrPacketTooLarge", err)
	}
}

func TestParseConnect(t *testing.T) {
	data := buildTestConnect("device-1", false, 45, "will/topic", []byte("offline"))
	p, err := readPacket(bufio.NewReader(bytes.NewReader(data)), 1<<20)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	req, err := parseConnect(p.payload)
	if err != nil {
		t.Fatalf("parse connect: %v", err)
	}
	if req.ClientID != "device-1" || req.KeepAlive != 45 || req.CleanSession {
		t.Fatalf("unexpected request: %+v", req)
	}
	if req.Will == nil || req.Will.Topic != "will/topic" || string(req.Will.Payload) != "offline" {
		t.Fatalf("unexpected will: %+v", req.Will)
	}
}

func TestParsePublishAndSubscribe(t *testing.T) {
	publish := buildPublish("devices/1/up", []byte("data"), 1, false, false, 7)
	message, err := parsePublish(publish)
	if err != nil {
		t.Fatalf("parse publish: %v", err)
	}
	if message.Topic != "devices/1/up" || message.PacketID != 7 || string(message.Payload) != "data" {
		t.Fatalf("unexpected message: %+v", message)
	}

	payload := []byte{0, 9}
	payload = append(payload, 0, 9)
	payload = append(payload, "devices/#"...)
	payload = append(payload, 1)
	packetID, subs, err := parseSubscribe(&packet{typ: packetSubscribe, flags: 0x02, payload: payload})
	if err != nil {
		t.Fatalf("parse subscribe: %v", err)
	}
	if packetID != 9 || len(subs) != 1 || subs[0].Filter != "devices/#" || subs[0].QoS != 1 {
		t.Fatalf("unexpected subscribe: id=%d subs=%+v", packetID, subs)
	}

	// 非法过滤器：# 必须位于最后一层
	invalid := []byte{0, 1, 0, 3, 'a', '#', 'b', 0}
	if _, _, err := parseSubscribe(&packet{typ: packetSubscribe, flags: 0x02, payload: invalid}); err == nil {
		t.Fatal("expected invalid filter error")
	}
}

func TestUnsupportedProtocolVersion(t *testing.T) {
	payload := []byte{0, 4, 'M', 'Q', 'T', 'T', 5, 0x02, 0, 30, 0, 1, 'x'}
	if _, err := parseConnect(payload); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol", err)
	}
}
