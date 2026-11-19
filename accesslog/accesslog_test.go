package accesslog

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/logger"
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

func newTestLogger(t *testing.T) (logger.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.log")
	log, err := logger.New(logger.Options{Level: "debug", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log, path
}

func readTestLog(t *testing.T, log logger.Logger, path string) string {
	t.Helper()
	if err := log.Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func putIdentity(c *gin.Context, identity *auth.Identity) {
	c.Set(auth.IdentityKey, identity)
	c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), identity))
}

func withIdentity(identity *auth.Identity) gin.HandlerFunc {
	return func(c *gin.Context) {
		putIdentity(c, identity)
		c.Next()
	}
}

func withSpanContext(spanContext oteltrace.SpanContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(oteltrace.ContextWithSpanContext(c.Request.Context(), spanContext))
		c.Next()
	}
}

func TestAccessLogRecordsRequest(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(Middleware(log))
	engine.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	recorder := doRequest(engine, http.MethodGet, "/ok", nil, map[string]string{"X-Request-ID": "rid-log"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	content := readTestLog(t, log, path)
	for _, expected := range []string{`"message":"http request"`, `"status":200`, `"request_id":"rid-log"`, `"method":"GET"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("log does not contain %q: %s", expected, content)
		}
	}
}

func TestAccessLogSkipsConfiguredPaths(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(Middleware(log, "/health"))
	engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	doRequest(engine, http.MethodGet, "/health", nil, nil)
	content := readTestLog(t, log, path)
	if strings.Contains(content, "http request") {
		t.Fatalf("expected health path to be skipped: %s", content)
	}
}

func TestAccessLogRouteSkipAndIdentity(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(Middleware(log, "/items/:id"))
	engine.GET("/items/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	doRequest(engine, http.MethodGet, "/items/42", nil, nil)
	if content := readTestLog(t, log, path); strings.Contains(content, "http request") {
		t.Fatalf("route should be skipped: %s", content)
	}

	traceID, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{TraceID: traceID, SpanID: spanID})

	identity := &auth.Identity{UserID: "u9", TenantID: "t9"}
	tracedLog, tracedPath := newTestLogger(t)
	traced := newTestEngine(
		withSpanContext(spanContext),
		withIdentity(identity),
		Middleware(tracedLog),
	)
	traced.GET("/traced", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	doRequest(traced, http.MethodGet, "/traced", nil, nil)

	content := readTestLog(t, tracedLog, tracedPath)
	for _, expected := range []string{`"user_id":"u9"`, `"tenant_id":"t9"`, `"trace_id":"` + traceID.String() + `"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("log missing %q: %s", expected, content)
		}
	}
}
