package middleware

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimeoutWriterPassthrough(t *testing.T) {
	fake := newFakeResponseWriter()
	w := &timeoutWriter{ResponseWriter: fake}

	if w.Status() != http.StatusOK || w.Size() != 0 || w.Written() {
		t.Fatal("unexpected initial state")
	}
	w.WriteHeader(http.StatusCreated)
	if fake.status != http.StatusCreated || !w.Written() {
		t.Fatalf("status = %d, written = %v", fake.status, w.Written())
	}
	w.WriteHeaderNow()
	if n, err := w.WriteString("body"); err != nil || n != 4 {
		t.Fatalf("write string = %d, %v", n, err)
	}
	if w.Size() != 4 || fake.body.String() != "body" {
		t.Fatalf("size = %d body = %q", w.Size(), fake.body.String())
	}
	w.Flush()
	if fake.flushCount != 1 {
		t.Fatalf("flush count = %d", fake.flushCount)
	}
	if _, _, err := w.Hijack(); err != nil {
		t.Fatalf("hijack: %v", err)
	}
	if w.CloseNotify() != fake.closeCh {
		t.Fatal("CloseNotify should delegate")
	}
	if w.Pusher() != nil {
		t.Fatal("Pusher should delegate")
	}
	// 已写出响应后超时只标记状态，不覆盖已有响应。
	w.timeout([]byte("timeout"))
	if fake.body.String() != "body" {
		t.Fatalf("timeout overwrote body: %q", fake.body.String())
	}
}

func TestTimeoutWriterDropsLateWrites(t *testing.T) {
	fake := newFakeResponseWriter()
	w := &timeoutWriter{ResponseWriter: fake}

	w.timeout([]byte("timed out"))
	if fake.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", fake.status)
	}
	if !strings.Contains(fake.body.String(), "timed out") {
		t.Fatalf("body = %q", fake.body.String())
	}
	if fake.flushCount != 1 {
		t.Fatalf("flush count = %d", fake.flushCount)
	}

	// 重复超时直接返回。
	w.timeout([]byte("again"))
	if strings.Count(fake.body.String(), "timed out") != 1 {
		t.Fatalf("body = %q", fake.body.String())
	}

	w.WriteHeader(http.StatusOK)
	w.WriteHeaderNow()
	if _, err := w.Write([]byte("late")); err != http.ErrHandlerTimeout {
		t.Fatalf("write error = %v", err)
	}
	w.Flush()
	if fake.flushCount != 1 {
		t.Fatalf("flush after timeout = %d", fake.flushCount)
	}
	if _, _, err := w.Hijack(); err != http.ErrHandlerTimeout {
		t.Fatalf("hijack error = %v", err)
	}
	if fake.status != http.StatusServiceUnavailable || strings.Contains(fake.body.String(), "late") {
		t.Fatalf("late write leaked: %d %q", fake.status, fake.body.String())
	}
}

func TestTimeoutKeepsEarlierResponse(t *testing.T) {
	engine := newTestEngine(Timeout(10 * time.Millisecond))
	engine.GET("/early", func(c *gin.Context) {
		c.String(http.StatusOK, "early")
		time.Sleep(40 * time.Millisecond)
	})

	recorder := doRequest(engine, http.MethodGet, "/early", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "early" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestTimeoutWriterFlushBeforeTimeout(t *testing.T) {
	engine := newTestEngine(Timeout(time.Second))
	engine.GET("/flush", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString("chunk")
		c.Writer.Flush()
		c.Status(http.StatusOK)
	})

	recorder := doRequest(engine, http.MethodGet, "/flush", nil, nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "chunk" {
		t.Fatalf("unexpected response: %d %q", recorder.Code, recorder.Body.String())
	}
}
