package broker

import (
	"bufio"
	"bytes"
	"errors"
	"testing"
)

func TestReadPacketErrors(t *testing.T) {
	if _, err := readPacket(bufio.NewReader(bytes.NewReader(nil)), 1<<20); err == nil {
		t.Fatal("expected EOF error")
	}

	// 剩余长度最多 4 字节，全为续位时视为非法。
	malformed := []byte{0x30, 0x80, 0x80, 0x80, 0x80}
	if _, err := readPacket(bufio.NewReader(bytes.NewReader(malformed)), 1<<20); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("err = %v, want ErrMalformedPacket", err)
	}

	// 声明长度大于实际数据。
	truncated := []byte{0x30, 0x05, 'a'}
	if _, err := readPacket(bufio.NewReader(bytes.NewReader(truncated)), 1<<20); err == nil {
		t.Fatal("expected truncated payload error")
	}
}

func TestParseConnectErrors(t *testing.T) {
	valid := connectSpec{clientID: "device", clean: true}

	cases := map[string]func(*connectSpec){
		"empty payload":      func(*connectSpec) {},
		"protocol level 0":   func(s *connectSpec) { s.level = 2 },
		"will qos 3":         func(s *connectSpec) { s.willTopic = "t"; s.willQoS = 3 },
		"reserved flag":      func(s *connectSpec) { s.forceFlags = true; s.flags = 0x01 },
		"will without flag":  func(s *connectSpec) { s.forceFlags = true; s.flags = 0x08 },
		"empty non-clean id": func(s *connectSpec) { s.clientID = ""; s.clean = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := valid
			mutate(&spec)
			payload := spec.packet().payload
			if name == "empty payload" {
				payload = nil
			}
			if name == "protocol level 0" {
				payload = []byte{0, 4, 'M', 'Q', 'T', 'T', 2}
			}
			if _, err := parseConnect(payload); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}

	// MQTT 3.1.1 允许密码携带但不带用户名。
	passwordOnly := connectSpec{clientID: "device", clean: true, passwordOnly: true, password: "secret"}
	if _, err := parseConnect(passwordOnly.packet().payload); err != nil {
		t.Fatalf("password-only connect: %v", err)
	}

	full := connectSpec{
		clientID: "device", clean: true, keepAlive: 30,
		username: "user", password: "pass",
		willTopic: "will/topic", willPayload: []byte("bye"), willQoS: 1, willRetain: true,
	}
	req, err := parseConnect(full.packet().payload)
	if err != nil {
		t.Fatalf("parseConnect: %v", err)
	}
	if req.Username != "user" || string(req.Password) != "pass" {
		t.Fatalf("unexpected credentials: %+v", req)
	}
	if req.Will == nil || req.Will.Topic != "will/topic" || !req.Will.Retain || req.Will.QoS != 1 {
		t.Fatalf("unexpected will: %+v", req.Will)
	}

	// 客户端 ID 为空且 clean=true 时由 Broker 自动生成，解析仍然通过。
	anonymous := connectSpec{clean: true}
	if _, err := parseConnect(anonymous.packet().payload); err != nil {
		t.Fatalf("anonymous connect: %v", err)
	}
}

func TestParsePublishErrors(t *testing.T) {
	if _, err := parsePublish(&packet{typ: packetPublish, flags: 0x06, payload: []byte{0, 1, 'a'}}); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("err = %v, want ErrMalformedPacket", err)
	}
	if _, err := parsePublish(buildPublish("", []byte("x"), 0, false, false, 0)); err == nil {
		t.Fatal("expected empty topic error")
	}
	if _, err := parsePublish(buildPublish("a/+", []byte("x"), 0, false, false, 0)); err == nil {
		t.Fatal("expected wildcard topic error")
	}
	if _, err := parsePublish(&packet{typ: packetPublish, flags: 0x02, payload: []byte{0, 1, 'a', 0, 0}}); err == nil {
		t.Fatal("expected zero packet id error")
	}
	if _, err := parsePublish(&packet{typ: packetPublish, flags: 0x02, payload: []byte{0, 1, 'a'}}); err == nil {
		t.Fatal("expected truncated packet id error")
	}
}

func TestParseSubscribeErrors(t *testing.T) {
	cases := []*packet{
		{typ: packetSubscribe, flags: 0x00, payload: []byte{0, 1, 0, 1, 'a', 0}},
		{typ: packetSubscribe, flags: 0x0A, payload: []byte{0, 1, 0, 1, 'a', 0}},
		{typ: packetSubscribe, flags: 0x02, payload: []byte{0, 0, 0, 1, 'a', 0}},
		{typ: packetSubscribe, flags: 0x02, payload: []byte{0, 1}},
		{typ: packetSubscribe, flags: 0x02, payload: []byte{0, 1, 0, 1, 'a', 0x80}},
		{typ: packetSubscribe, flags: 0x02, payload: []byte{0, 1, 0, 1, 'a'}},
	}
	for i, p := range cases {
		if _, _, err := parseSubscribe(p); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestParseUnsubscribe(t *testing.T) {
	p := buildUnsubscribePacket(5, "a/b", "c/#")
	id, filters, err := parseUnsubscribe(p)
	if err != nil {
		t.Fatalf("parseUnsubscribe: %v", err)
	}
	if id != 5 || len(filters) != 2 || filters[1] != "c/#" {
		t.Fatalf("id=%d filters=%v", id, filters)
	}

	cases := []*packet{
		{typ: packetUnsubscribe, flags: 0x00, payload: []byte{0, 1, 0, 1, 'a'}},
		{typ: packetUnsubscribe, flags: 0x0A, payload: []byte{0, 1, 0, 1, 'a'}},
		{typ: packetUnsubscribe, flags: 0x02, payload: []byte{0, 0, 0, 1, 'a'}},
		{typ: packetUnsubscribe, flags: 0x02, payload: []byte{0, 1}},
		{typ: packetUnsubscribe, flags: 0x02, payload: []byte{0, 1, 0, 2, 'a'}},
	}
	for i, p := range cases {
		if _, _, err := parseUnsubscribe(p); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestParsePacketIDErrors(t *testing.T) {
	if _, err := parsePacketID(&packet{payload: []byte{0}}); err == nil {
		t.Fatal("expected short payload error")
	}
	if _, err := parsePacketID(&packet{payload: []byte{0, 0}}); err == nil {
		t.Fatal("expected zero id error")
	}
	id, err := parsePacketID(&packet{payload: []byte{0, 9}})
	if err != nil || id != 9 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}

func TestPacketReaderErrors(t *testing.T) {
	reader := &packetReader{}
	if _, err := reader.readByte(); err == nil {
		t.Fatal("expected readByte error")
	}
	if _, err := reader.readUint16(); err == nil {
		t.Fatal("expected readUint16 error")
	}

	// 非法 UTF-8 与 NUL 字符都应被拒绝。
	invalidUTF8 := &packetReader{data: []byte{0, 2, 0xff, 0xfe}}
	if _, err := invalidUTF8.readString(); err == nil {
		t.Fatal("expected utf8 error")
	}
	withNull := &packetReader{data: []byte{0, 3, 'a', 0, 'b'}}
	if _, err := withNull.readString(); err == nil {
		t.Fatal("expected null error")
	}
	tooLong := &packetReader{data: []byte{0, 5, 'a'}}
	if _, err := tooLong.readBinary(); err == nil {
		t.Fatal("expected binary length error")
	}

	full := &packetReader{data: []byte{0, 2, 'o', 'k'}}
	value, err := full.readString()
	if err != nil || value != "ok" {
		t.Fatalf("readString = %q, %v", value, err)
	}
	if remaining := full.remainingLen(); remaining != 0 {
		t.Fatalf("remaining = %d", remaining)
	}
}

func TestBuildHelperPackets(t *testing.T) {
	unsuback := buildUnsuback(3)
	if unsuback.typ != packetUnsuback {
		t.Fatalf("type = %d", unsuback.typ)
	}
	pingresp := buildPingresp()
	if pingresp.typ != packetPingresp || len(pingresp.payload) != 0 {
		t.Fatalf("unexpected pingresp: %+v", pingresp)
	}
	connack := buildConnack(true, 0)
	if connack.payload[0] != 1 {
		t.Fatalf("session present = %d", connack.payload[0])
	}
}

func TestTopicMatchesEdges(t *testing.T) {
	if topicMatches("a/b", "a") {
		t.Fatal("longer filter must not match shorter topic")
	}
	if topicMatches("a/b", "a/b/c") {
		t.Fatal("shorter filter must not match longer topic")
	}
	if !topicMatches("$SYS/#", "$SYS/uptime") {
		t.Fatal("system topic prefix should match")
	}
}
