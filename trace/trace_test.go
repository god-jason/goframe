package trace

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Endpoint = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected empty endpoint error")
	}
	cfg = DefaultConfig()
	cfg.SampleRatio = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected sample ratio error")
	}
	cfg = DefaultConfig()
	cfg.Protocol = "udp"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected protocol error")
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if GetConfig() == nil {
		t.Fatal("current config is nil")
	}
}

func TestMiddlewarePropagatesTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	engine := gin.New()
	engine.Use(Middleware())
	engine.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(HeaderTraceID); got != traceID {
		t.Fatalf("trace id = %q, want %q", got, traceID)
	}
	if len(exporter.GetSpans()) != 1 {
		t.Fatalf("spans = %d, want 1", len(exporter.GetSpans()))
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestHTTPTransportInjectsTraceparent(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &http.Client{Transport: HTTPTransport(server.Client().Transport), Timeout: 3 * time.Second}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = response.Body.Close()
	if received == "" {
		t.Fatal("traceparent was not injected")
	}
}

// setTraceConfig 替换包内配置读取函数，测试结束后恢复。
func setTraceConfig(t *testing.T, c Config) {
	t.Helper()
	previous := currentConfig
	currentConfig = func() *Config { return &c }
	t.Cleanup(func() { currentConfig = previous })
}

// resetTraceProvider 清理全局 TracerProvider，避免测试相互影响。
func resetTraceProvider(t *testing.T) {
	t.Helper()
	global.provider = nil
	otel.SetTracerProvider(noop.NewTracerProvider())
}

func TestTraceIDEmpty(t *testing.T) {
	if got := TraceID(context.Background()); got != "" {
		t.Fatalf("trace id = %q, want empty", got)
	}
}

func TestMiddlewareUnmatchedAndServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	engine := gin.New()
	engine.Use(Middleware())
	engine.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	engine.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	unmatched := httptest.NewRecorder()
	engine.ServeHTTP(unmatched, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if unmatched.Code != http.StatusNotFound {
		t.Fatalf("unmatched status = %d", unmatched.Code)
	}

	boom := httptest.NewRecorder()
	engine.ServeHTTP(boom, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if boom.Code != http.StatusInternalServerError {
		t.Fatalf("boom status = %d", boom.Code)
	}

	ok := httptest.NewRecorder()
	engine.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("ok status = %d", ok.Code)
	}
}

func TestScheme(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)
	if got := scheme(request); got != "http" {
		t.Fatalf("scheme = %q, want http", got)
	}
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := scheme(request); got != "https" {
		t.Fatalf("scheme = %q, want forwarded https", got)
	}
	request.TLS = &tls.ConnectionState{}
	if got := scheme(request); got != "https" {
		t.Fatalf("scheme = %q, want tls https", got)
	}
}

type stubRoundTripper struct {
	response *http.Response
	err      error
}

func (s stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return s.response, s.err
}

func TestHTTPTransportBranches(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	if HTTPTransport(nil) == nil {
		t.Fatal("nil base should fall back to default transport")
	}

	request := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)

	_, err := HTTPTransport(stubRoundTripper{err: errors.New("boom")}).RoundTrip(request)
	if err == nil {
		t.Fatal("expected transport error")
	}

	failed := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Status:     "500 Internal Server Error",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
	response, err := HTTPTransport(stubRoundTripper{response: failed}).RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("response = %+v, err = %v", response, err)
	}

	ok := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
	if _, err := HTTPTransport(stubRoundTripper{response: ok}).RoundTrip(request); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

func TestNewExporterProtocols(t *testing.T) {
	cases := []Config{
		{Protocol: "", Endpoint: "localhost:4318", Insecure: true, Timeout: time.Second},
		{Protocol: "http", Endpoint: "localhost:4318", Insecure: false, Timeout: time.Second},
		{Protocol: "http", Endpoint: "localhost:4318", Insecure: true, Headers: map[string]string{"k": "v"}, Timeout: time.Second},
		{Protocol: "grpc", Endpoint: "localhost:4317", Insecure: true, Timeout: time.Second},
		{Protocol: "grpc", Endpoint: "localhost:4317", Insecure: false, Timeout: time.Second},
		{Protocol: "grpc", Endpoint: "localhost:4317", Insecure: true, Headers: map[string]string{"k": "v"}, Timeout: time.Second},
	}
	for _, c := range cases {
		exporter, err := newExporter(context.Background(), c)
		if err != nil {
			t.Fatalf("newExporter(%s, insecure=%v): %v", c.Protocol, c.Insecure, err)
		}
		if exporter == nil {
			t.Fatalf("exporter for %q is nil", c.Protocol)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_ = exporter.Shutdown(ctx)
		cancel()
	}
}

func TestNewExporterErrors(t *testing.T) {
	previousHTTP, previousGRPC := newHTTPExporter, newGRPCExporter
	t.Cleanup(func() {
		newHTTPExporter = previousHTTP
		newGRPCExporter = previousGRPC
	})

	newHTTPExporter = func(context.Context, ...otlptracehttp.Option) (sdktrace.SpanExporter, error) {
		return nil, errors.New("http boom")
	}
	if _, err := newExporter(context.Background(), Config{Protocol: "http", Endpoint: "x"}); err == nil {
		t.Fatal("expected http exporter error")
	}

	newGRPCExporter = func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
		return nil, errors.New("grpc boom")
	}
	if _, err := newExporter(context.Background(), Config{Protocol: "grpc", Endpoint: "x"}); err == nil {
		t.Fatal("expected grpc exporter error")
	}
}

func TestStartupDisabled(t *testing.T) {
	setTraceConfig(t, Config{Enabled: false})
	resetTraceProvider(t)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("provider should be nil when disabled")
	}
}

func TestStartupValidateError(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.Protocol = "udp"
	setTraceConfig(t, c)
	resetTraceProvider(t)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected validate error")
	}
}

func TestStartupExporterError(t *testing.T) {
	previous := newHTTPExporter
	t.Cleanup(func() { newHTTPExporter = previous })
	newHTTPExporter = func(context.Context, ...otlptracehttp.Option) (sdktrace.SpanExporter, error) {
		return nil, errors.New("boom")
	}

	c := DefaultConfig()
	c.Enabled = true
	c.Protocol = "http"
	setTraceConfig(t, c)
	resetTraceProvider(t)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected exporter error")
	}
}

func TestStartupHTTPAndShutdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := DefaultConfig()
	c.Enabled = true
	c.Protocol = "http"
	c.Endpoint = strings.TrimPrefix(server.URL, "http://")
	c.Insecure = true
	c.Headers = map[string]string{"authorization": "test"}
	c.ServiceName = ""
	c.SampleRatio = 0.5
	c.Timeout = 2 * time.Second
	c.BatchTimeout = 10 * time.Millisecond
	setTraceConfig(t, c)
	resetTraceProvider(t)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("provider is nil after startup")
	}
	if err := Shutdown(nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("provider should be nil after shutdown")
	}
}

func TestStartupServiceNameAndShutdownDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := DefaultConfig()
	c.Enabled = true
	c.Protocol = "http"
	c.Endpoint = strings.TrimPrefix(server.URL, "http://")
	c.Insecure = true
	c.ServiceName = "svc"
	c.Timeout = 2 * time.Second
	c.BatchTimeout = 10 * time.Millisecond
	setTraceConfig(t, c)
	resetTraceProvider(t)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestStartupGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	c := DefaultConfig()
	c.Enabled = true
	c.Protocol = "grpc"
	c.Endpoint = listener.Addr().String()
	c.Insecure = true
	c.Timeout = 100 * time.Millisecond
	c.BatchTimeout = 10 * time.Millisecond
	setTraceConfig(t, c)
	resetTraceProvider(t)

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	// 本地监听未实现 OTLP 服务，Shutdown 可能超时，这里只验证不会 panic 且状态被清理。
	_ = Shutdown(context.Background())
	if Get() != nil {
		t.Fatal("provider should be nil after shutdown")
	}
}

func TestShutdownWithoutProvider(t *testing.T) {
	resetTraceProvider(t)
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

type failingExporter struct{}

func (failingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (failingExporter) Shutdown(context.Context) error                             { return errors.New("shutdown boom") }

func TestShutdownError(t *testing.T) {
	resetTraceProvider(t)
	global.provider = sdktrace.NewTracerProvider(sdktrace.WithSyncer(failingExporter{}))

	if err := Shutdown(context.Background()); err == nil {
		t.Fatal("expected shutdown error")
	}
}
