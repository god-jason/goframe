package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// gzipConfig 是 Gzip 中间件配置。
type gzipConfig struct {
	minSize int
	level   int
}

// GzipOption 是 Gzip 中间件选项。
type GzipOption func(*gzipConfig)

// GzipMinSize 设置触发压缩的最小响应体字节数，小于该值时不压缩。
func GzipMinSize(size int) GzipOption {
	return func(c *gzipConfig) {
		if size >= 0 {
			c.minSize = size
		}
	}
}

// GzipLevel 设置 gzip 压缩级别，取值参考 compress/gzip 常量。
func GzipLevel(level int) GzipOption {
	return func(c *gzipConfig) {
		c.level = level
	}
}

// Gzip 对响应做 gzip 压缩。
// 已有 Content-Encoding、WebSocket、SSE 与 HEAD 请求直接跳过；
// 小于最小尺寸的响应保持明文，避免压缩小包反而增加开销。
// 压缩时移除 Content-Length 并追加 Vary: Accept-Encoding。
func Gzip(opts ...GzipOption) gin.HandlerFunc {
	cfg := gzipConfig{minSize: 1024, level: gzip.DefaultCompression}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return func(c *gin.Context) {
		if skipGzip(c) {
			c.Next()
			return
		}

		header := c.Writer.Header()
		header.Set("Vary", addVary(header.Get("Vary"), "Accept-Encoding"))

		writer := &gzipWriter{
			ResponseWriter: c.Writer,
			minSize:        cfg.minSize,
			level:          cfg.level,
		}
		c.Writer = writer
		defer func() { _ = writer.Close() }()
		c.Next()
	}
}

// skipGzip 判断当前请求是否应该跳过压缩。
func skipGzip(c *gin.Context) bool {
	if c.Request.Method == http.MethodHead {
		return true
	}
	// 上游已经编码的响应不能再压缩，否则会破坏 Content-Encoding 语义。
	if c.Writer.Header().Get("Content-Encoding") != "" {
		return true
	}
	// Upgrade 表示协议切换（WebSocket、h2c 等），响应不是普通文本流。
	if strings.TrimSpace(c.GetHeader("Upgrade")) != "" {
		return true
	}
	// SSE 需要逐条事件即时推送，压缩缓冲会破坏实时性。
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream") {
		return true
	}
	return false
}

// addVary 向 Vary 头追加值，已存在时保持原值。
func addVary(current, value string) string {
	for _, part := range strings.Split(current, ",") {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return current
		}
	}
	if strings.TrimSpace(current) == "" {
		return value
	}
	return current + ", " + value
}

// isEventStream 判断响应类型是否为 SSE。
func isEventStream(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

// gzipWriter 延迟压缩决定，用于实现最小压缩尺寸。
// 在超过 minSize 之前数据留在内存缓冲区，之后切换为 gzip 流。
type gzipWriter struct {
	gin.ResponseWriter

	minSize int
	level   int

	buf    bytes.Buffer
	gz     *gzip.Writer
	status int
	// statusSet 表示业务已显式或隐式写入状态码。
	statusSet bool
	// headerSent 表示响应头已经写入底层 writer。
	headerSent bool
	closed     bool
	size       int
}

// WriteHeader 记录状态码，真正写出延迟到压缩策略确定之后。
// 1xx/204/304 没有响应体，立即按明文写出。
func (w *gzipWriter) WriteHeader(code int) {
	if w.headerSent || w.closed {
		return
	}
	w.status = code
	w.statusSet = true
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified {
		w.headerSent = true
		w.ResponseWriter.WriteHeader(code)
	}
}

// Write 写入响应体。未达到最小尺寸时先缓冲。
func (w *gzipWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("gzip: response writer is closed")
	}
	if !w.statusSet {
		w.status = http.StatusOK
	}
	w.size += len(p)

	if w.headerSent {
		if w.gz != nil {
			return w.gz.Write(p)
		}
		return w.ResponseWriter.Write(p)
	}

	if isEventStream(w.Header().Get("Content-Type")) {
		if err := w.startPlain(); err != nil {
			return 0, err
		}
		return w.ResponseWriter.Write(p)
	}

	if w.buf.Len()+len(p) < w.minSize {
		w.buf.Write(p)
		return len(p), nil
	}

	if err := w.startGzip(); err != nil {
		// 压缩器创建失败时降级为明文，不影响业务响应。
		if plainErr := w.startPlain(); plainErr != nil {
			return 0, plainErr
		}
		return w.ResponseWriter.Write(p)
	}
	return w.gz.Write(p)
}

// WriteString 实现 gin.ResponseWriter。
func (w *gzipWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// WriteHeaderNow 立即写出响应头。此时尚无压缩决定时按明文处理。
func (w *gzipWriter) WriteHeaderNow() {
	if w.headerSent || w.closed {
		return
	}
	if !w.statusSet {
		w.status = http.StatusOK
	}
	if w.buf.Len() > 0 && w.buf.Len() >= w.minSize {
		if err := w.startGzip(); err == nil {
			return
		}
	}
	_ = w.startPlain()
}

// Flush 支持流式响应。一旦调用 Flush，说明调用方期望立即发送，立即启用压缩。
func (w *gzipWriter) Flush() {
	if w.closed {
		return
	}
	if !w.headerSent {
		if !w.statusSet {
			w.status = http.StatusOK
		}
		if isEventStream(w.Header().Get("Content-Type")) {
			_ = w.startPlain()
		} else if err := w.startGzip(); err != nil {
			_ = w.startPlain()
		}
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// Status 返回当前状态码。
func (w *gzipWriter) Status() int {
	if w.statusSet {
		return w.status
	}
	return http.StatusOK
}

// Size 返回接收到的未压缩响应体字节数。
func (w *gzipWriter) Size() int { return w.size }

// Written 返回响应头是否已经写出。
func (w *gzipWriter) Written() bool { return w.headerSent }

// Hijack 透传底层连接。协议切换场景已在中间件入口跳过压缩。
func (w *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.closed = true
	w.headerSent = true
	return w.ResponseWriter.Hijack()
}

// CloseNotify 透传连接关闭通知。
func (w *gzipWriter) CloseNotify() <-chan bool { return w.ResponseWriter.CloseNotify() }

// Pusher 透传 HTTP/2 Server Push。
func (w *gzipWriter) Pusher() http.Pusher { return w.ResponseWriter.Pusher() }

// startPlain 写出未压缩响应头，并冲刷已缓冲的数据。
func (w *gzipWriter) startPlain() error {
	if w.headerSent {
		return nil
	}
	if !w.statusSet {
		w.status = http.StatusOK
	}
	w.headerSent = true
	w.ResponseWriter.WriteHeader(w.status)
	if w.buf.Len() > 0 {
		if _, err := w.buf.WriteTo(w.ResponseWriter); err != nil {
			return err
		}
	}
	return nil
}

// startGzip 写出压缩响应头并冲刷已缓冲的数据。
func (w *gzipWriter) startGzip() error {
	if w.headerSent {
		return nil
	}
	if !w.statusSet {
		w.status = http.StatusOK
	}

	writer, err := gzip.NewWriterLevel(w.ResponseWriter, w.level)
	if err != nil {
		return fmt.Errorf("gzip: create writer: %w", err)
	}

	header := w.Header()
	header.Del("Content-Length")
	header.Set("Content-Encoding", "gzip")
	header.Set("Vary", addVary(header.Get("Vary"), "Accept-Encoding"))

	w.headerSent = true
	w.ResponseWriter.WriteHeader(w.status)
	w.gz = writer
	if w.buf.Len() > 0 {
		if _, err := writer.Write(w.buf.Bytes()); err != nil {
			return err
		}
		w.buf.Reset()
	}
	return nil
}

// Close 结束压缩流或写出未压缩的缓冲数据，可重复调用。
func (w *gzipWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.gz != nil {
		return w.gz.Close()
	}
	if w.headerSent {
		return nil
	}
	return w.startPlain()
}
