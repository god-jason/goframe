package middleware

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimeoutReturns503OnSlowHandler(t *testing.T) {
	engine := newTestEngine(Timeout(20 * time.Millisecond))

	handlerDone := make(chan struct{})
	engine.GET("/slow", func(c *gin.Context) {
		defer close(handlerDone)
		time.Sleep(80 * time.Millisecond)
		_, _ = c.Writer.WriteString("late")
	})

	recorder := doRequest(engine, http.MethodGet, "/slow", nil, nil)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "request timeout") {
		t.Fatalf("body = %q", recorder.Body.String())
	}

	// 等待处理器结束，确认超时后的写入已被丢弃。
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	if strings.Contains(recorder.Body.String(), "late") {
		t.Fatalf("late write leaked into response: %q", recorder.Body.String())
	}
}

func TestTimeoutAllowsFastHandler(t *testing.T) {
	engine := newTestEngine(Timeout(time.Second))
	engine.GET("/fast", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	recorder := doRequest(engine, http.MethodGet, "/fast", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "ok" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestTimeoutDisabledForNonPositiveDuration(t *testing.T) {
	engine := newTestEngine(Timeout(0))
	engine.GET("/ok", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	recorder := doRequest(engine, http.MethodGet, "/ok", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestTimeoutCancelsRequestContext(t *testing.T) {
	engine := newTestEngine(Timeout(20 * time.Millisecond))
	canceled := make(chan struct{})

	engine.GET("/ctx", func(c *gin.Context) {
		select {
		case <-c.Request.Context().Done():
			close(canceled)
		case <-time.After(time.Second):
		}
	})

	recorder := doRequest(engine, http.MethodGet, "/ctx", nil, nil)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request context was not canceled")
	}
}
