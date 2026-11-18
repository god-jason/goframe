package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeResponseWriter 是可控的 gin.ResponseWriter 实现，用于直接驱动包装 writer。
type fakeResponseWriter struct {
	header     http.Header
	status     int
	size       int
	written    bool
	body       bytes.Buffer
	writeErr   error
	flushCount int
	hijackErr  error
	closeCh    chan bool
	pusher     http.Pusher
}

func newFakeResponseWriter() *fakeResponseWriter {
	return &fakeResponseWriter{
		header:  make(http.Header),
		status:  http.StatusOK,
		closeCh: make(chan bool),
	}
}

func (w *fakeResponseWriter) Header() http.Header { return w.header }

func (w *fakeResponseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.status = code
	w.written = true
}

func (w *fakeResponseWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	w.size += len(p)
	return w.body.Write(p)
}

func (w *fakeResponseWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *fakeResponseWriter) Status() int { return w.status }

func (w *fakeResponseWriter) Size() int { return w.size }

func (w *fakeResponseWriter) Written() bool { return w.written }

func (w *fakeResponseWriter) WriteHeaderNow() {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
}

func (w *fakeResponseWriter) Flush() { w.flushCount++ }

func (w *fakeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, w.hijackErr
}

func (w *fakeResponseWriter) CloseNotify() <-chan bool { return w.closeCh }

func (w *fakeResponseWriter) Pusher() http.Pusher { return w.pusher }

func testGzipWriter(fake ginResponseWriter) *gzipWriter {
	return &gzipWriter{ResponseWriter: fake, minSize: 1024, level: gzip.DefaultCompression}
}

// ginResponseWriter 别名让辅助函数签名更短。
type ginResponseWriter = *fakeResponseWriter

func TestGzipWriterBuffersThenClosesPlain(t *testing.T) {
	fake := newFakeResponseWriter()
	w := testGzipWriter(fake)

	if w.Status() != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Status())
	}
	if _, err := w.Write([]byte("small")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if w.Size() != 5 {
		t.Fatalf("size = %d, want 5", w.Size())
	}
	if w.Written() || fake.written {
		t.Fatal("response should stay buffered below min size")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !fake.written || fake.status != http.StatusOK {
		t.Fatalf("header not written: %d", fake.status)
	}
	if fake.body.String() != "small" {
		t.Fatalf("body = %q", fake.body.String())
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := w.Write([]byte("closed")); err == nil {
		t.Fatal("write after close should fail")
	}
	w.WriteHeader(http.StatusTeapot)
	w.WriteHeaderNow()
	w.Flush()
}

func TestGzipWriterWriteHeaderImmediateCodes(t *testing.T) {
	for _, code := range []int{http.StatusEarlyHints, http.StatusNoContent, http.StatusNotModified} {
		fake := newFakeResponseWriter()
		w := testGzipWriter(fake)
		w.WriteHeader(code)
		if !fake.written || fake.status != code {
			t.Fatalf("code %d not written immediately: %d", code, fake.status)
		}
		if !w.Written() {
			t.Fatalf("code %d should mark written", code)
		}
		w.WriteHeader(code)
		if _, err := w.Write([]byte("payload")); err != nil {
			t.Fatalf("write after immediate header: %v", err)
		}
		if fake.body.String() != "payload" {
			t.Fatalf("body = %q", fake.body.String())
		}
	}
}

func TestGzipWriterCompressesAndFlushes(t *testing.T) {
	fake := newFakeResponseWriter()
	fake.header.Set("Content-Length", "123")
	fake.header.Set("Vary", "Accept-Language")
	w := &gzipWriter{ResponseWriter: fake, minSize: 4, level: gzip.DefaultCompression}

	w.WriteHeader(http.StatusCreated)
	if w.Status() != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Status())
	}
	if _, err := w.Write([]byte(strings.Repeat("a", 100))); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := fake.header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content encoding = %q", got)
	}
	if got := fake.header.Get("Content-Length"); got != "" {
		t.Fatalf("content length = %q, want removed", got)
	}
	if got := fake.header.Get("Vary"); got != "Accept-Language, Accept-Encoding" {
		t.Fatalf("vary = %q", got)
	}
	if fake.status != http.StatusCreated {
		t.Fatalf("status = %d", fake.status)
	}
	w.Flush()
	if fake.flushCount != 1 {
		t.Fatalf("flush count = %d", fake.flushCount)
	}
	if _, err := w.WriteString("tail"); err != nil {
		t.Fatalf("write string: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reader, err := gzip.NewReader(bytes.NewReader(fake.body.Bytes()))
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if want := strings.Repeat("a", 100) + "tail"; string(decoded) != want {
		t.Fatalf("decoded body mismatch: %d bytes", len(decoded))
	}
}

func TestGzipWriterSSEContentTypeStaysPlain(t *testing.T) {
	fake := newFakeResponseWriter()
	fake.header.Set("Content-Type", "text/event-stream")
	w := &gzipWriter{ResponseWriter: fake, minSize: 1, level: gzip.DefaultCompression}

	if _, err := w.Write([]byte("data: hi\n\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := fake.header.Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q", got)
	}
	if fake.body.String() != "data: hi\n\n" {
		t.Fatalf("body = %q", fake.body.String())
	}
}

func TestGzipWriterWriteHeaderNow(t *testing.T) {
	// closed writer
	closed := &gzipWriter{ResponseWriter: newFakeResponseWriter()}
	closed.closed = true
	closed.WriteHeaderNow()

	// already sent header
	sent := &gzipWriter{ResponseWriter: newFakeResponseWriter()}
	sent.headerSent = true
	sent.WriteHeaderNow()

	// buffered below min size -> plain
	fakePlain := newFakeResponseWriter()
	plain := &gzipWriter{ResponseWriter: fakePlain, minSize: 100, level: gzip.DefaultCompression}
	_, _ = plain.Write([]byte("buffered"))
	plain.WriteHeaderNow()
	if !fakePlain.written || fakePlain.body.String() != "buffered" {
		t.Fatalf("plain buffer not flushed: %q", fakePlain.body.String())
	}

	// buffered above min size -> gzip
	fakeGzip := newFakeResponseWriter()
	compressed := &gzipWriter{ResponseWriter: fakeGzip, minSize: 100, level: gzip.DefaultCompression}
	_, _ = compressed.Write([]byte("buffered"))
	compressed.minSize = 4
	compressed.WriteHeaderNow()
	if got := fakeGzip.header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content encoding = %q", got)
	}

	// gzip creation failure falls back to plain
	fakeFallback := newFakeResponseWriter()
	fallback := &gzipWriter{ResponseWriter: fakeFallback, minSize: 100, level: 99}
	_, _ = fallback.Write([]byte("buffered"))
	fallback.minSize = 4
	fallback.WriteHeaderNow()
	if got := fakeFallback.header.Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q", got)
	}
	if fakeFallback.body.String() != "buffered" {
		t.Fatalf("fallback body = %q", fakeFallback.body.String())
	}
}

func TestGzipWriterStartErrors(t *testing.T) {
	writeErr := stderrors.New("write failed")

	plain := &gzipWriter{ResponseWriter: newFakeResponseWriter()}
	plain.headerSent = true
	if err := plain.startPlain(); err != nil {
		t.Fatalf("startPlain after header: %v", err)
	}

	failingPlainFake := newFakeResponseWriter()
	failingPlainFake.writeErr = writeErr
	failingPlain := &gzipWriter{ResponseWriter: failingPlainFake}
	_, _ = failingPlain.buf.WriteString("data")
	if err := failingPlain.startPlain(); err == nil {
		t.Fatal("startPlain should surface write error")
	}

	sentGzip := &gzipWriter{ResponseWriter: newFakeResponseWriter()}
	sentGzip.headerSent = true
	if err := sentGzip.startGzip(); err != nil {
		t.Fatalf("startGzip after header: %v", err)
	}

	invalid := &gzipWriter{ResponseWriter: newFakeResponseWriter(), level: 99}
	if err := invalid.startGzip(); err == nil {
		t.Fatal("invalid level should fail")
	}

	failingBufFake := newFakeResponseWriter()
	failingBufFake.writeErr = writeErr
	failingBuf := &gzipWriter{ResponseWriter: failingBufFake, level: gzip.DefaultCompression}
	_, _ = failingBuf.buf.WriteString("data")
	if err := failingBuf.startGzip(); err == nil {
		t.Fatal("startGzip should surface buffered write error")
	}

	fallbackFake := newFakeResponseWriter()
	fallback := &gzipWriter{ResponseWriter: fallbackFake, minSize: 1, level: 99}
	if _, err := fallback.Write([]byte("hello")); err != nil {
		t.Fatalf("write fallback: %v", err)
	}
	if fallbackFake.body.String() != "hello" {
		t.Fatalf("fallback body = %q", fallbackFake.body.String())
	}
	if got := fallbackFake.header.Get("Content-Encoding"); got != "" {
		t.Fatalf("fallback encoding = %q", got)
	}
}

func TestGzipWriterCloseAfterHeader(t *testing.T) {
	fake := newFakeResponseWriter()
	w := testGzipWriter(fake)
	w.WriteHeader(http.StatusNoContent)
	if err := w.Close(); err != nil {
		t.Fatalf("close after header: %v", err)
	}
	if fake.status != http.StatusNoContent {
		t.Fatalf("status = %d", fake.status)
	}
}

func TestGzipWriterPassthroughMethods(t *testing.T) {
	fake := newFakeResponseWriter()
	w := testGzipWriter(fake)

	if _, _, err := w.Hijack(); err != nil {
		t.Fatalf("hijack: %v", err)
	}
	if !w.closed || !w.Written() {
		t.Fatal("hijack should mark writer closed")
	}
	if w.CloseNotify() != fake.closeCh {
		t.Fatal("CloseNotify should delegate")
	}
	if w.Pusher() != nil {
		t.Fatal("Pusher should delegate")
	}
}

func TestGzipWriterFlushBeforeHeader(t *testing.T) {
	// SSE 走明文。
	sseFake := newFakeResponseWriter()
	sseFake.header.Set("Content-Type", "text/event-stream")
	sse := &gzipWriter{ResponseWriter: sseFake, minSize: 1, level: gzip.DefaultCompression}
	sse.Flush()
	if !sseFake.written || sseFake.header.Get("Content-Encoding") != "" {
		t.Fatalf("sse flush: written=%v encoding=%q", sseFake.written, sseFake.header.Get("Content-Encoding"))
	}

	// 压缩器创建失败降级为明文。
	fallbackFake := newFakeResponseWriter()
	fallback := &gzipWriter{ResponseWriter: fallbackFake, minSize: 1, level: 99}
	fallback.Flush()
	if !fallbackFake.written || fallbackFake.header.Get("Content-Encoding") != "" {
		t.Fatalf("fallback flush: written=%v encoding=%q", fallbackFake.written, fallbackFake.header.Get("Content-Encoding"))
	}

	// 正常启用压缩。
	gzipFake := newFakeResponseWriter()
	compressed := &gzipWriter{ResponseWriter: gzipFake, minSize: 1, level: gzip.DefaultCompression}
	compressed.Flush()
	if got := gzipFake.header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content encoding = %q", got)
	}
}

func TestGzipWriterWriteErrors(t *testing.T) {
	writeErr := stderrors.New("write failed")

	// SSE 分支冲刷缓冲失败。
	sseFake := newFakeResponseWriter()
	sse := &gzipWriter{ResponseWriter: sseFake, minSize: 100, level: gzip.DefaultCompression}
	if _, err := sse.Write([]byte("buffered")); err != nil {
		t.Fatalf("buffer write: %v", err)
	}
	sseFake.header.Set("Content-Type", "text/event-stream")
	sseFake.writeErr = writeErr
	if _, err := sse.Write([]byte("data")); err == nil {
		t.Fatal("sse flush should surface write error")
	}

	// 压缩器创建失败且明文降级也失败。
	fallbackFake := newFakeResponseWriter()
	fallback := &gzipWriter{ResponseWriter: fallbackFake, minSize: 100, level: gzip.DefaultCompression}
	if _, err := fallback.Write([]byte("buffered")); err != nil {
		t.Fatalf("buffer write: %v", err)
	}
	fallback.minSize = 4
	fallback.level = 99
	fallbackFake.writeErr = writeErr
	if _, err := fallback.Write([]byte("more")); err == nil {
		t.Fatal("plain fallback should surface write error")
	}
}

func TestGzipLevelOption(t *testing.T) {
	cfg := gzipConfig{}
	GzipLevel(gzip.BestSpeed)(&cfg)
	if cfg.level != gzip.BestSpeed {
		t.Fatalf("level = %d", cfg.level)
	}
	if Gzip(GzipLevel(gzip.BestSpeed)) == nil {
		t.Fatal("Gzip returned nil handler")
	}
}

func TestAddVary(t *testing.T) {
	if got := addVary("", "Accept-Encoding"); got != "Accept-Encoding" {
		t.Fatalf("empty vary = %q", got)
	}
	if got := addVary("Accept-Encoding", "accept-encoding"); got != "Accept-Encoding" {
		t.Fatalf("duplicate vary = %q", got)
	}
	if got := addVary("Accept-Language", "Accept-Encoding"); got != "Accept-Language, Accept-Encoding" {
		t.Fatalf("appended vary = %q", got)
	}
}

func TestGzipSkipsHeadRequest(t *testing.T) {
	engine := newTestEngine(Gzip(GzipMinSize(1)))
	engine.HEAD("/head", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString("head body")
	})

	recorder := doRequest(engine, http.MethodHead, "/head", nil, nil)
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q", got)
	}
}
