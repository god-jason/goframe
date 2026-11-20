package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/storage"
)

// startCloseServer 启动一个接受连接后延迟关闭的假 clamd 服务。
func startCloseServer(t *testing.T, delay time.Duration) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(delay)
				_ = c.Close()
			}()
		}
	}()
	return l.Addr().String()
}

// TestClamAVScanContextCancelClosesConn 覆盖扫描中上下文取消触发 AfterFunc 关闭连接（clamav.go:70）。
func TestClamAVScanContextCancelClosesConn(t *testing.T) {
	addr := startCloseServer(t, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := NewClamAV(ClamAVConfig{Addr: addr, Timeout: 5 * time.Second}).
		Scan(ctx, []byte("data"), "text/plain")
	if err == nil {
		t.Fatal("expected error after cancel")
	}
}

// startResetAfterDrainServer 启动假 clamd：随机排空 D 字节后立即以 SO_LINGER 0 关闭（发 RST）。
// 客户端应用写入在偏移 D+发送缓冲 处阻塞，随机 D 使阻塞点以约 32768/32772 的概率
// 落在 32KB 分块数据写入上，从而触发数据写入失败分支。
func startResetAfterDrainServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				drain := 32786 + rand.Intn(32772)
				buf := make([]byte, drain)
				_, err := io.ReadFull(c, buf)
				if err != nil {
					_ = c.Close()
					return
				}
				if tc, ok := c.(*net.TCPConn); ok {
					_ = tc.SetLinger(0) // 必须在 Close 前设置，确保发 RST 而非 FIN
				}
				_ = c.Close()
			}()
		}
	}()
	return l.Addr().String()
}

// TestClamAVScanChunkWriteTimeout 覆盖分块数据写入失败的分支（clamav.go:90）。
// 若个别时序下阻塞点恰落在 4 字节长度头写入，循环重试。
func TestClamAVScanChunkWriteTimeout(t *testing.T) {
	addr := startResetAfterDrainServer(t)
	data := bytes.Repeat([]byte("A"), 2<<20)
	scanner := NewClamAV(ClamAVConfig{Addr: addr, Timeout: 2 * time.Second})
	var seen []string
	for i := 0; i < 10; i++ {
		err := scanner.Scan(context.Background(), data, "application/octet-stream")
		if err == nil {
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, "write clamd chunk") && !strings.Contains(msg, "chunk size") {
			return
		}
		seen = append(seen, msg)
	}
	t.Fatalf("未能在重试内触发分块数据写入失败分支, errors=%q", seen)
}

// TestClamAVScanTerminatorOrReadError 覆盖终止块写入失败或读取响应失败的分支（clamav.go:96/102）。
func TestClamAVScanTerminatorOrReadError(t *testing.T) {
	addr := startCloseServer(t, 0)
	seenTerminator, seenRead := false, false
	for i := 0; i < 50 && !(seenTerminator && seenRead); i++ {
		err := NewClamAV(ClamAVConfig{Addr: addr, Timeout: time.Second}).
			Scan(context.Background(), nil, "text/plain")
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "write clamd terminator") {
			seenTerminator = true
		}
		if strings.Contains(err.Error(), "read clamd response") {
			seenRead = true
		}
	}
	if !seenTerminator || !seenRead {
		t.Skipf("平台时序未同时触发两个分支: terminator=%v read=%v", seenTerminator, seenRead)
	}
}

// peekFailReader 首次读满 512 字节（含指定前缀）使 Peek 成功，后续读报错。
type peekFailReader struct {
	header  []byte
	firsted bool
}

func (r *peekFailReader) Read(p []byte) (int, error) {
	if !r.firsted {
		r.firsted = true
		return copy(p, r.header), nil
	}
	return 0, errors.New("read boom")
}

func pngHeader() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 512-8)...)
}

func textHeader() []byte {
	return bytes.Repeat([]byte("hello world\n"), 512/12+1)[:512]
}

// TestSaveImageReadLimitedError 覆盖图片分支 readLimited 失败（upload.go:192）。
func TestSaveImageReadLimitedError(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig())
	_, err := service.Save(context.Background(), "t", "docs", "a.png",
		&peekFailReader{header: pngHeader()})
	if err == nil || !strings.Contains(err.Error(), "read file failed") {
		t.Fatalf("err = %v", err)
	}
}

// TestSaveScanReadLimitedError 覆盖扫描分支 readLimited 失败（upload.go:224）。
func TestSaveScanReadLimitedError(t *testing.T) {
	service, _ := newTestService(t, testUploadConfig(), WithScanner(acceptScanner{}))
	service.scan = true
	_, err := service.Save(context.Background(), "t", "docs", "a.txt",
		&peekFailReader{header: textHeader()})
	if err == nil || !strings.Contains(err.Error(), "read file failed") {
		t.Fatalf("err = %v", err)
	}
}

// TestBootStartupCreatesClamAVScanner 覆盖未注入扫描器但开启扫描时自动创建 clamav 扫描器（boot.go:49）。
func TestBootStartupCreatesClamAVScanner(t *testing.T) {
	t.Setenv("GOFRAME_STORAGE_DRIVER", "local")
	t.Setenv("GOFRAME_STORAGE_PATH", t.TempDir())
	t.Setenv("GOFRAME_SECURITY_UPLOAD_SCAN_ENABLED", "true")
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	defer config.Reload()
	if err := storage.Startup(context.Background()); err != nil {
		t.Fatalf("storage.Startup: %v", err)
	}
	defer func() { _ = storage.Shutdown(context.Background()) }()

	SetScanner(nil)
	defer SetScanner(nil)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	defer func() { _ = Shutdown(context.Background()) }()
	if Get() == nil {
		t.Fatal("service should be created")
	}
}

// TestBootStartupNewError 覆盖 New 失败时 Startup 返回错误（boot.go:58）。
func TestBootStartupNewError(t *testing.T) {
	t.Setenv("GOFRAME_STORAGE_DRIVER", "local")
	t.Setenv("GOFRAME_STORAGE_PATH", t.TempDir())
	t.Setenv("GOFRAME_SECURITY_UPLOAD_MAX_SIZE", "0")
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	defer config.Reload()
	if err := storage.Startup(context.Background()); err != nil {
		t.Fatalf("storage.Startup: %v", err)
	}
	defer func() { _ = storage.Shutdown(context.Background()) }()

	SetScanner(nil)
	defer SetScanner(nil)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected New error")
	}
}
