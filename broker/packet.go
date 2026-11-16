// Package broker 实现嵌入式 MQTT 3.1 / 3.1.1 Broker。
//
// 能力：
//   - CONNECT / PUBLISH / SUBSCRIBE / UNSUBSCRIBE / PING / DISCONNECT；
//   - QoS 0/1/2 基本握手；
//   - 通配符订阅（+ / #）与 $ 主题规则；
//   - Retained 消息；
//   - 清理会话与持久会话（离线订阅 + 有限离线队列）；
//   - 同 ClientID 连接接管；
//   - Will 消息；
//   - 鉴权回调、连接/发布/订阅 Hook、运行统计；
//   - TCP 与 TLS 监听。
//
// 不支持 MQTT 5 属性、共享订阅、WebSocket 传输与集群；如需这些能力，
// 建议使用 EMQX、Mosquitto 等独立 Broker。
package broker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	packetConnect     byte = 1
	packetConnack     byte = 2
	packetPublish     byte = 3
	packetPuback      byte = 4
	packetPubrec      byte = 5
	packetPubrel      byte = 6
	packetPubcomp     byte = 7
	packetSubscribe   byte = 8
	packetSuback      byte = 9
	packetUnsubscribe byte = 10
	packetUnsuback    byte = 11
	packetPingreq     byte = 12
	packetPingresp    byte = 13
	packetDisconnect  byte = 14
)

const (
	protocolLevel31  byte = 3
	protocolLevel311 byte = 4
)

var (
	// ErrMalformedPacket 表示协议报文非法。
	ErrMalformedPacket = errors.New("broker: malformed packet")
	// ErrPacketTooLarge 表示报文超过限制。
	ErrPacketTooLarge = errors.New("broker: packet too large")
	// ErrUnsupportedProtocol 表示不支持的 MQTT 版本。
	ErrUnsupportedProtocol = errors.New("broker: unsupported protocol version")
)

type packet struct {
	typ     byte
	flags   byte
	payload []byte
}

func (p *packet) qos() byte      { return (p.flags >> 1) & 0x03 }
func (p *packet) dup() bool      { return p.flags&0x08 != 0 }
func (p *packet) retained() bool { return p.flags&0x01 != 0 }

// readPacket 从流中读取一个 MQTT 报文。
func readPacket(r *bufio.Reader, maxSize int) (*packet, error) {
	header, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	remaining, err := readRemainingLength(r)
	if err != nil {
		return nil, err
	}
	if remaining < 0 || (maxSize > 0 && remaining > maxSize) {
		return nil, ErrPacketTooLarge
	}
	payload := make([]byte, remaining)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return &packet{typ: header >> 4, flags: header & 0x0F, payload: payload}, nil
}

func readRemainingLength(r *bufio.Reader) (int, error) {
	value := 0
	multiplier := 1
	for i := 0; i < 4; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(b&0x7F) * multiplier
		if b&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, ErrMalformedPacket
}

func encodePacket(p *packet) []byte {
	length := len(p.payload)
	buf := make([]byte, 0, 1+4+length)
	buf = append(buf, p.typ<<4|p.flags)
	// 剩余长度可变编码。
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		buf = append(buf, digit)
		if length == 0 {
			break
		}
	}
	return append(buf, p.payload...)
}

// ConnectRequest 是解析后的 CONNECT 请求。
type ConnectRequest struct {
	ProtocolLevel byte
	ClientID      string
	Username      string
	Password      []byte
	CleanSession  bool
	KeepAlive     uint16
	Will          *Message
	RemoteAddr    string
}

// Message 是 Broker 内部与 Hook 使用的消息。
type Message struct {
	Topic    string
	Payload  []byte
	QoS      byte
	Retain   bool
	Dup      bool
	PacketID uint16
}

// Subscription 是一条订阅。
type Subscription struct {
	Filter string
	QoS    byte
}

func parseConnect(payload []byte) (*ConnectRequest, error) {
	reader := &packetReader{data: payload}
	protocolName, err := reader.readString()
	if err != nil {
		return nil, err
	}
	level, err := reader.readByte()
	if err != nil {
		return nil, err
	}
	if level != protocolLevel31 && level != protocolLevel311 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedProtocol, level)
	}
	if level == protocolLevel311 && protocolName != "MQTT" {
		return nil, ErrMalformedPacket
	}
	if level == protocolLevel31 && protocolName != "MQIsdp" {
		return nil, ErrMalformedPacket
	}

	flags, err := reader.readByte()
	if err != nil {
		return nil, err
	}
	if flags&0x01 != 0 {
		return nil, ErrMalformedPacket
	}
	cleanSession := flags&0x02 != 0
	willFlag := flags&0x04 != 0
	willQoS := (flags >> 3) & 0x03
	willRetain := flags&0x20 != 0
	passwordFlag := flags&0x40 != 0
	usernameFlag := flags&0x80 != 0
	if !willFlag && (willQoS != 0 || willRetain) {
		return nil, ErrMalformedPacket
	}
	if willQoS > 2 {
		return nil, ErrMalformedPacket
	}
	if passwordFlag && !usernameFlag {
		// MQTT 3.1.1 允许密码不带用户名，但多数客户端不会这样；保持兼容。
	}

	keepAlive, err := reader.readUint16()
	if err != nil {
		return nil, err
	}
	clientID, err := reader.readString()
	if err != nil {
		return nil, err
	}
	if clientID == "" && !cleanSession {
		return nil, ErrMalformedPacket
	}

	var will *Message
	if willFlag {
		topic, err := reader.readString()
		if err != nil {
			return nil, err
		}
		message, err := reader.readBinary()
		if err != nil {
			return nil, err
		}
		if err := validateTopicName(topic); err != nil {
			return nil, err
		}
		will = &Message{Topic: topic, Payload: message, QoS: willQoS, Retain: willRetain}
	}

	username := ""
	if usernameFlag {
		username, err = reader.readString()
		if err != nil {
			return nil, err
		}
	}
	var password []byte
	if passwordFlag {
		password, err = reader.readBinary()
		if err != nil {
			return nil, err
		}
	}

	return &ConnectRequest{
		ProtocolLevel: level,
		ClientID:      clientID,
		Username:      username,
		Password:      password,
		CleanSession:  cleanSession,
		KeepAlive:     keepAlive,
		Will:          will,
	}, nil
}

func parsePublish(p *packet) (*Message, error) {
	if p.qos() > 2 {
		return nil, ErrMalformedPacket
	}
	reader := &packetReader{data: p.payload}
	topic, err := reader.readString()
	if err != nil {
		return nil, err
	}
	if err := validateTopicName(topic); err != nil {
		return nil, err
	}
	message := &Message{
		Topic:  topic,
		QoS:    p.qos(),
		Retain: p.retained(),
		Dup:    p.dup(),
	}
	if message.QoS > 0 {
		packetID, err := reader.readUint16()
		if err != nil {
			return nil, err
		}
		if packetID == 0 {
			return nil, ErrMalformedPacket
		}
		message.PacketID = packetID
	}
	message.Payload = append([]byte(nil), reader.remaining()...)
	return message, nil
}

func parseSubscribe(p *packet) (uint16, []Subscription, error) {
	if p.qos() != 1 || p.dup() {
		return 0, nil, ErrMalformedPacket
	}
	reader := &packetReader{data: p.payload}
	packetID, err := reader.readUint16()
	if err != nil || packetID == 0 {
		return 0, nil, ErrMalformedPacket
	}
	var subscriptions []Subscription
	for reader.remainingLen() > 0 {
		filter, err := reader.readString()
		if err != nil {
			return 0, nil, err
		}
		qos, err := reader.readByte()
		if err != nil {
			return 0, nil, err
		}
		if qos&0xFC != 0 || qos > 2 {
			return 0, nil, ErrMalformedPacket
		}
		if err := validateFilter(filter); err != nil {
			return 0, nil, err
		}
		subscriptions = append(subscriptions, Subscription{Filter: filter, QoS: qos})
	}
	if len(subscriptions) == 0 {
		return 0, nil, ErrMalformedPacket
	}
	return packetID, subscriptions, nil
}

func parseUnsubscribe(p *packet) (uint16, []string, error) {
	if p.qos() != 1 || p.dup() {
		return 0, nil, ErrMalformedPacket
	}
	reader := &packetReader{data: p.payload}
	packetID, err := reader.readUint16()
	if err != nil || packetID == 0 {
		return 0, nil, ErrMalformedPacket
	}
	var filters []string
	for reader.remainingLen() > 0 {
		filter, err := reader.readString()
		if err != nil {
			return 0, nil, err
		}
		filters = append(filters, filter)
	}
	if len(filters) == 0 {
		return 0, nil, ErrMalformedPacket
	}
	return packetID, filters, nil
}

func parsePacketID(p *packet) (uint16, error) {
	if len(p.payload) < 2 {
		return 0, ErrMalformedPacket
	}
	id := binary.BigEndian.Uint16(p.payload[:2])
	if id == 0 {
		return 0, ErrMalformedPacket
	}
	return id, nil
}

func buildConnack(sessionPresent bool, code byte) *packet {
	first := byte(0)
	if sessionPresent {
		first = 1
	}
	return &packet{typ: packetConnack, payload: []byte{first, code}}
}

func buildPublish(topic string, payload []byte, qos byte, retain, dup bool, packetID uint16) *packet {
	buf := bytes.NewBuffer(nil)
	writeString(buf, topic)
	if qos > 0 {
		_ = binary.Write(buf, binary.BigEndian, packetID)
	}
	buf.Write(payload)

	flags := qos << 1
	if retain {
		flags |= 0x01
	}
	if dup {
		flags |= 0x08
	}
	return &packet{typ: packetPublish, flags: flags, payload: buf.Bytes()}
}

func buildAck(typ byte, packetID uint16) *packet {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, packetID)
	flags := byte(0)
	if typ == packetPubrel {
		flags = 0x02
	}
	return &packet{typ: typ, flags: flags, payload: payload}
}

func buildSuback(packetID uint16, codes []byte) *packet {
	payload := make([]byte, 2+len(codes))
	binary.BigEndian.PutUint16(payload[:2], packetID)
	copy(payload[2:], codes)
	return &packet{typ: packetSuback, payload: payload}
}

func buildUnsuback(packetID uint16) *packet {
	return buildAck(packetUnsuback, packetID)
}

func buildPingresp() *packet {
	return &packet{typ: packetPingresp}
}

func writeString(buf *bytes.Buffer, value string) {
	_ = binary.Write(buf, binary.BigEndian, uint16(len(value)))
	buf.WriteString(value)
}

type packetReader struct {
	data   []byte
	offset int
}

func (r *packetReader) remainingLen() int { return len(r.data) - r.offset }

func (r *packetReader) remaining() []byte { return r.data[r.offset:] }

func (r *packetReader) readByte() (byte, error) {
	if r.offset >= len(r.data) {
		return 0, ErrMalformedPacket
	}
	value := r.data[r.offset]
	r.offset++
	return value, nil
}

func (r *packetReader) readUint16() (uint16, error) {
	if r.offset+2 > len(r.data) {
		return 0, ErrMalformedPacket
	}
	value := binary.BigEndian.Uint16(r.data[r.offset : r.offset+2])
	r.offset += 2
	return value, nil
}

func (r *packetReader) readString() (string, error) {
	data, err := r.readBinary()
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", ErrMalformedPacket
	}
	value := string(data)
	if containsNull(value) {
		return "", ErrMalformedPacket
	}
	return value, nil
}

func (r *packetReader) readBinary() ([]byte, error) {
	length, err := r.readUint16()
	if err != nil {
		return nil, err
	}
	if r.offset+int(length) > len(r.data) {
		return nil, ErrMalformedPacket
	}
	value := r.data[r.offset : r.offset+int(length)]
	r.offset += int(length)
	return value, nil
}
