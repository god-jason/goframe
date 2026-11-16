package middleware

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	headers := HeadersConfig{
		Enabled:    true,
		HSTS:       true,
		HSTSMaxAge: time.Hour,
		CSP:        "default-src 'none'",
	}
	engine := newTestEngine(SecurityHeaders(headers))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/", nil, nil)
	expected := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
		"Content-Security-Policy": "default-src 'none'",
	}
	for header, want := range expected {
		if got := recorder.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if got := recorder.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=3600") {
		t.Fatalf("hsts = %q", got)
	}
}

func TestMethodFilterBlocksTraceAndUnlistedMethods(t *testing.T) {
	engine := newTestEngine(MethodFilter([]string{http.MethodGet}))
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", recorder.Code)
	}
	for _, method := range []string{http.MethodTrace, http.MethodPut} {
		recorder := doRequest(engine, method, "/ok", nil, nil)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, recorder.Code)
		}
		if recorder.Header().Get("Allow") == "" {
			t.Fatalf("%s response missing Allow header", method)
		}
	}
}

func TestRequestLimits(t *testing.T) {
	cfg := RequestConfig{
		MaxBodySize:     10,
		MaxQueryLength:  8,
		RequireJSONBody: true,
	}
	engine := newTestEngine(RequestLimits(cfg))
	engine.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })

	cases := []struct {
		name        string
		target      string
		body        string
		contentType string
		wantStatus  int
	}{
		{"valid json", "/submit", "{}", "application/json", http.StatusOK},
		{"query too long", "/submit?verylongquery=1", "{}", "application/json", http.StatusBadRequest},
		{"body too large", "/submit", strings.Repeat("a", 20), "application/json", http.StatusRequestEntityTooLarge},
		{"unsupported content type", "/submit", "a=1", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := doRequest(engine, http.MethodPost, tc.target, strings.NewReader(tc.body), map[string]string{
				"Content-Type": tc.contentType,
			})
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestCORSAllowedOriginAndPreflight(t *testing.T) {
	cfg := CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{http.MethodGet, http.MethodPost},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           time.Hour,
	}
	handler, err := CORS(cfg)
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	engine := newTestEngine(handler)
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/ok", nil, map[string]string{"Origin": "https://app.example.com"})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
		t.Fatalf("expose headers = %q", got)
	}

	preflight := doRequest(engine, http.MethodOptions, "/ok", nil, map[string]string{
		"Origin":                        "https://app.example.com",
		"Access-Control-Request-Method": http.MethodGet,
	})
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflight.Code)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("allow methods = %q", got)
	}
	if got := preflight.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Fatalf("max age = %q", got)
	}
}

func TestCORSRejectsDisallowedOrigin(t *testing.T) {
	handler, err := CORS(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
	})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	engine := newTestEngine(handler)
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/ok", nil, map[string]string{"Origin": "https://evil.example.com"})
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin must not receive CORS headers")
	}

	preflight := doRequest(engine, http.MethodOptions, "/ok", nil, map[string]string{"Origin": "https://evil.example.com"})
	if preflight.Code != http.StatusForbidden {
		t.Fatalf("preflight status = %d, want 403", preflight.Code)
	}
}

func TestCORSRejectsWildcardWithCredentials(t *testing.T) {
	_, err := CORS(CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
	if err == nil {
		t.Fatal("expected wildcard with credentials to be rejected")
	}
}

func TestRecoveryReturnsSafe500(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(Recovery(log))
	engine.GET("/panic", func(*gin.Context) { panic("unexpected failure") })

	recorder := doRequest(engine, http.MethodGet, "/panic", nil, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "unexpected failure") {
		t.Fatal("panic details must not be returned to the client")
	}
	content := readTestLog(t, log, path)
	if !strings.Contains(content, "panic recovered") || !strings.Contains(content, "stack") {
		t.Fatalf("expected panic log with stack, got %s", content)
	}
}

func TestAccessLogRecordsRequest(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(RequestID(), AccessLog(log))
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
	engine := newTestEngine(AccessLog(log, "/health"))
	engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	doRequest(engine, http.MethodGet, "/health", nil, nil)
	content := readTestLog(t, log, path)
	if strings.Contains(content, "http request") {
		t.Fatalf("expected health path to be skipped: %s", content)
	}
}
