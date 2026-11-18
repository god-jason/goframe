package upload

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/storage"
)

func TestClamAVDefaults(t *testing.T) {
	scanner := NewClamAV(ClamAVConfig{})
	if scanner.addr != clamAVDefaultAddr || scanner.timeout != clamAVDefaultTimeout {
		t.Fatalf("scanner = %+v", scanner)
	}
	scanner = NewClamAV(ClamAVConfig{Addr: " 127.0.0.1:1234 ", Timeout: time.Second})
	if scanner.addr != "127.0.0.1:1234" || scanner.timeout != time.Second {
		t.Fatalf("scanner = %+v", scanner)
	}
}

func TestClamAVDialError(t *testing.T) {
	scanner := NewClamAV(ClamAVConfig{Addr: "127.0.0.1:1", Timeout: 500 * time.Millisecond})
	if err := scanner.Scan(context.Background(), []byte("x"), "text/plain"); err == nil {
		t.Fatal("expected dial error")
	}
}

func TestClamAVServerClosesWithoutResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	scanner := NewClamAV(ClamAVConfig{Addr: listener.Addr().String(), Timeout: time.Second})
	if err := scanner.Scan(context.Background(), []byte("x"), "text/plain"); err == nil {
		t.Fatal("expected read/write error")
	}
}

func TestParseClamAVResponse(t *testing.T) {
	if err := parseClamAVResponse(""); err == nil {
		t.Fatal("expected empty response error")
	}
	if err := parseClamAVResponse("OK\x00"); err != nil {
		t.Fatalf("OK: %v", err)
	}
	if err := parseClamAVResponse("stream: OK\x00"); err != nil {
		t.Fatalf("stream OK: %v", err)
	}
	if err := parseClamAVResponse("stream: FOUND\x00"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("found without name: %v", err)
	}
	if err := parseClamAVResponse("some error\x00"); err == nil || !strings.Contains(err.Error(), "scan failed") {
		t.Fatalf("error response: %v", err)
	}
}

func TestWriteAllAndClamAVError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeAll(ctx, io.Discard, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := clamAVError(ctx, "action", errors.New("boom")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := clamAVError(context.Background(), "action", errors.New("boom")); err == nil || !strings.Contains(err.Error(), "action") {
		t.Fatalf("err = %v", err)
	}
}

func TestBootLifecycle(t *testing.T) {
	t.Setenv("GOFRAME_STORAGE_DRIVER", "local")
	t.Setenv("GOFRAME_STORAGE_PATH", t.TempDir())
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	ctx := context.Background()
	if err := storage.Startup(ctx); err != nil {
		t.Fatalf("storage.Startup: %v", err)
	}
	defer func() { _ = storage.Shutdown(ctx) }()

	SetScanner(nil)
	defer SetScanner(nil)

	if GetConfig() == nil {
		t.Fatal("GetConfig should not be nil")
	}
	if Get() != nil {
		t.Fatal("Get should be nil before Startup")
	}
	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("Get should return service after Startup")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("Get should be nil after Shutdown")
	}

	// 未设置扫描器时显式注入，验证选项分支。
	SetScanner(acceptScanner{})
	if err := Startup(ctx); err != nil {
		t.Fatalf("Startup with scanner: %v", err)
	}
	_ = Shutdown(ctx)
}

func TestBootStartupWithoutStorage(t *testing.T) {
	// 确保全局存储为空时 Startup 直接返回。
	_ = storage.Shutdown(context.Background())
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
}
