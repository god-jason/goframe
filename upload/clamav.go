package upload

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	clamAVDefaultAddr    = "127.0.0.1:3310"
	clamAVDefaultTimeout = 30 * time.Second
	clamAVChunkSize      = 32 << 10
	clamAVMaxResponse    = 4 << 10
)

// ClamAVConfig 是 clamd 扫描器配置。
type ClamAVConfig struct {
	Addr    string        `mapstructure:"addr"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// DefaultClamAVConfig 返回 clamd 默认配置。
func DefaultClamAVConfig() ClamAVConfig {
	return ClamAVConfig{
		Addr:    clamAVDefaultAddr,
		Timeout: clamAVDefaultTimeout,
	}
}

// ClamAV 是基于 clamd INSTREAM 协议的病毒扫描器。
type ClamAV struct {
	addr    string
	timeout time.Duration
}

var _ Scanner = (*ClamAV)(nil)

// NewClamAV 创建 clamd 扫描器。Addr 为空时使用默认地址，Timeout 不大于 0 时使用默认值。
func NewClamAV(cfg ClamAVConfig) *ClamAV {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = clamAVDefaultAddr
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = clamAVDefaultTimeout
	}
	return &ClamAV{addr: addr, timeout: timeout}
}

// Scan 通过 clamd INSTREAM 协议扫描内容，发现病毒时返回包含病毒名的错误。
func (c *ClamAV) Scan(ctx context.Context, data []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("upload: connect clamd %s: %w", c.addr, err)
	}
	defer conn.Close()

	// 上下文取消时立即关闭连接，解除阻塞中的读写。
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	if err := writeAll(ctx, conn, []byte("zINSTREAM\x00")); err != nil {
		return clamAVError(ctx, "write clamd command", err)
	}

	for offset := 0; offset < len(data); offset += clamAVChunkSize {
		end := offset + clamAVChunkSize
		if end > len(data) {
			end = len(data)
		}
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(end-offset))
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
		if err := writeAll(ctx, conn, size[:]); err != nil {
			return clamAVError(ctx, "write clamd chunk size", err)
		}
		if err := writeAll(ctx, conn, data[offset:end]); err != nil {
			return clamAVError(ctx, "write clamd chunk", err)
		}
	}

	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	if err := writeAll(ctx, conn, []byte{0, 0, 0, 0}); err != nil {
		return clamAVError(ctx, "write clamd terminator", err)
	}

	reader := bufio.NewReader(io.LimitReader(conn, clamAVMaxResponse))
	response, err := reader.ReadString(0)
	if err != nil && response == "" {
		return clamAVError(ctx, "read clamd response", err)
	}
	return parseClamAVResponse(response)
}

// writeAll 保证完整写入，并在每次写入前检查上下文。
func writeAll(ctx context.Context, w io.Writer, p []byte) error {
	for len(p) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// clamAVError 在上下文已取消时优先返回取消错误。
func clamAVError(ctx context.Context, action string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("upload: %s: %w", action, err)
}

// parseClamAVResponse 解析 clamd 返回，例如 "stream: OK" 或 "stream: Eicar-Test-Signature FOUND"。
func parseClamAVResponse(raw string) error {
	response := strings.TrimSpace(strings.TrimRight(raw, "\x00"))
	response = strings.TrimSpace(strings.TrimRight(response, "\r\n"))
	switch {
	case response == "":
		return fmt.Errorf("upload: clamd returned empty response")
	case strings.HasSuffix(response, " OK") || response == "OK":
		return nil
	case strings.HasSuffix(response, " FOUND"):
		name := strings.TrimSpace(strings.TrimSuffix(response, "FOUND"))
		name = strings.TrimSpace(strings.TrimPrefix(name, "stream:"))
		if name == "" {
			name = "unknown"
		}
		return fmt.Errorf("upload: virus detected: %s", name)
	default:
		return fmt.Errorf("upload: clamd scan failed: %s", response)
	}
}
