package idempotency

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
)

func newTestEngine(middlewares ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewares...)
	return engine
}

func doRequest(engine *gin.Engine, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

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
