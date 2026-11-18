package upload

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type clamdRequest struct {
	command string
	payload []byte
}

// startFakeClamd 启动一个假的 clamd 服务，记录收到的 INSTREAM 数据并返回固定响应。
func startFakeClamd(t *testing.T, response string) (string, <-chan clamdRequest) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	received := make(chan clamdRequest, 1)

	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		reader := bufio.NewReader(conn)
		command, err := reader.ReadString(0)
		if err != nil {
			return
		}
		var payload bytes.Buffer
		for {
			var size uint32
			if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
				return
			}
			if size == 0 {
				break
			}
			chunk := make([]byte, size)
			if _, err := io.ReadFull(reader, chunk); err != nil {
				return
			}
			payload.Write(chunk)
		}
		received <- clamdRequest{command: command, payload: payload.Bytes()}
		_, _ = conn.Write([]byte(response))
	}()

	return listener.Addr().String(), received
}

func TestClamAVScanClean(t *testing.T) {
	addr, received := startFakeClamd(t, "stream: OK\x00")
	scanner := NewClamAV(ClamAVConfig{Addr: addr, Timeout: 2 * time.Second})

	data := bytes.Repeat([]byte("A"), clamAVChunkSize+10)
	if err := scanner.Scan(context.Background(), data, "application/octet-stream"); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	select {
	case request := <-received:
		if request.command != "zINSTREAM\x00" {
			t.Fatalf("command = %q", request.command)
		}
		if !bytes.Equal(request.payload, data) {
			t.Fatalf("payload length = %d, want %d", len(request.payload), len(data))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for clamd request")
	}
}

func TestClamAVScanFound(t *testing.T) {
	addr, _ := startFakeClamd(t, "stream: Eicar-Test-Signature FOUND\x00")
	scanner := NewClamAV(ClamAVConfig{Addr: addr, Timeout: 2 * time.Second})

	err := scanner.Scan(context.Background(), []byte("malware"), "text/plain")
	if err == nil {
		t.Fatal("expected virus detection error")
	}
	if !strings.Contains(err.Error(), "Eicar-Test-Signature") {
		t.Fatalf("error %q does not contain virus name", err)
	}
}

func TestClamAVScanCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewClamAV(ClamAVConfig{Addr: "127.0.0.1:1", Timeout: time.Second}).Scan(ctx, []byte("x"), "text/plain")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
