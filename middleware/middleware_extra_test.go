package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/security/csrf"
)

func withIdentity(identity *auth.Identity) gin.HandlerFunc {
	return func(c *gin.Context) {
		setIdentity(c, identity)
		c.Next()
	}
}

func withSpanContext(spanContext oteltrace.SpanContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(oteltrace.ContextWithSpanContext(c.Request.Context(), spanContext))
		c.Next()
	}
}

func TestAuthenticateAPIKeys(t *testing.T) {
	plain, key, err := auth.NewAPIKey("tests", "u1", "t1", nil, nil, 0)
	if err != nil {
		t.Fatalf("new api key: %v", err)
	}
	repository := auth.NewMemoryAPIKeyRepository()
	if err := repository.Create(context.Background(), key); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	auth.SetAPIKeyRepository(repository)
	t.Cleanup(func() { auth.SetAPIKeyRepository(nil) })

	engine := newTestEngine(Authenticate(nil))
	engine.GET("/who", identityHandler)

	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{auth.HeaderAPIKey: plain}); recorder.Body.String() != "u1" {
		t.Fatalf("api key header body = %q", recorder.Body.String())
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer " + plain}); recorder.Body.String() != "u1" {
		t.Fatalf("bearer api key body = %q", recorder.Body.String())
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{auth.HeaderAPIKey: "invalid"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid api key header status = %d", recorder.Code)
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer gf_invalid"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer api key status = %d", recorder.Code)
	}
	// 非 API Key 令牌且未配置认证器时匿名放行。
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer opaque"}); recorder.Body.String() != "anonymous" {
		t.Fatalf("nil authenticator body = %q", recorder.Body.String())
	}
}

func TestAuthenticateMalformedAuthorization(t *testing.T) {
	engine := newTestEngine(Authenticate(nil))
	engine.GET("/who", identityHandler)

	for _, header := range []string{"Basic abc", "Bearer", "Bearer   "} {
		if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": header}); recorder.Body.String() != "anonymous" {
			t.Fatalf("header %q body = %q, want anonymous", header, recorder.Body.String())
		}
	}
}

func TestTenantEdgeCases(t *testing.T) {
	resolver := &stubTenantResolver{member: true}

	anonymous := newTestEngine(Tenant(resolver))
	anonymous.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(anonymous, http.MethodGet, "/tenant", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d", recorder.Code)
	}

	noTenant := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}),
		Tenant(nil),
	)
	noTenant.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(noTenant, http.MethodGet, "/tenant", nil, map[string]string{"Authorization": "Bearer token"}); recorder.Code != http.StatusOK {
		t.Fatalf("missing tenant status = %d", recorder.Code)
	}

	failing := &stubTenantResolver{err: errors.New("resolver down")}
	failingEngine := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1", TenantID: "t1"}}),
		Tenant(failing),
	)
	failingEngine.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := doRequest(failingEngine, http.MethodGet, "/tenant", nil, map[string]string{
		"Authorization": "Bearer token",
		HeaderTenant:    "t2",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("resolver failure status = %d", recorder.Code)
	}
}

func TestRequirePermissionNilAuthorizer(t *testing.T) {
	engine := newTestEngine(
		withIdentity(&auth.Identity{UserID: "u1"}),
		RequirePermission(nil, "user:read", nil),
	)
	engine.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/users", nil, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestAccessLogRouteSkipAndIdentity(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(AccessLog(log, "/items/:id"))
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
		RequestID(),
		withSpanContext(spanContext),
		withIdentity(identity),
		AccessLog(tracedLog),
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

func TestCORSDisabled(t *testing.T) {
	handler, err := CORS(CORSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	engine := newTestEngine(handler)
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/ok", nil, map[string]string{"Origin": "https://app.example.com"})
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disabled CORS should not set headers")
	}
}

func TestCORSWildcardAndOrigins(t *testing.T) {
	handler, err := CORS(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"", "  HTTPS://APP.EXAMPLE.COM  ", "*"},
	})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	engine := newTestEngine(handler)
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.OPTIONS("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/ok", nil, map[string]string{"Origin": "https://app.example.com"}); recorder.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard allow origin = %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}

	restricted, err := CORS(CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"", "  HTTPS://APP.EXAMPLE.COM  "},
	})
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	restrictedEngine := newTestEngine(restricted)
	restrictedEngine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	restrictedEngine.OPTIONS("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(restrictedEngine, http.MethodGet, "/ok", nil, map[string]string{"Origin": "https://app.example.com"})
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow origin = %q", got)
	}

	// 无 Origin 请求直接放行。
	if recorder := doRequest(restrictedEngine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("no origin status = %d", recorder.Code)
	}

	// 未配置 AllowedHeaders 时回退到请求头。
	preflight := doRequest(restrictedEngine, http.MethodOptions, "/ok", nil, map[string]string{
		"Origin":                         "https://app.example.com",
		"Access-Control-Request-Headers": "X-Custom, X-Other",
	})
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflight.Code)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Headers"); got != "X-Custom, X-Other" {
		t.Fatalf("allow headers = %q", got)
	}
	if got := preflight.Header().Get("Access-Control-Max-Age"); got != "" {
		t.Fatalf("max age = %q, want empty", got)
	}
}

func TestCSRFNilProtector(t *testing.T) {
	engine := newTestEngine(CSRF(nil))
	engine.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(engine, http.MethodPost, "/submit", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestSecurityHeadersReportOnly(t *testing.T) {
	engine := newTestEngine(SecurityHeaders(HeadersConfig{
		CSP:           "default-src 'self'",
		CSPReportOnly: true,
	}))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/", nil, nil)
	if got := recorder.Header().Get("Content-Security-Policy-Report-Only"); got != "default-src 'self'" {
		t.Fatalf("report only csp = %q", got)
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("csp = %q, want empty", got)
	}
	if got := recorder.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("hsts = %q, want empty", got)
	}
}

func TestMethodFilterDefaultMethods(t *testing.T) {
	engine := newTestEngine(MethodFilter(nil))
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", recorder.Code)
	}
	if recorder := doRequest(engine, http.MethodTrace, "/ok", nil, nil); recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("TRACE status = %d", recorder.Code)
	}
}

func TestRequestLimitsDisabledAndJSONSuffix(t *testing.T) {
	unlimited := newTestEngine(RequestLimits(RequestConfig{}))
	unlimited.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := doRequest(unlimited, http.MethodPost, "/submit", strings.NewReader("a=1"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("unlimited status = %d", recorder.Code)
	}

	jsonOnly := newTestEngine(RequestLimits(RequestConfig{RequireJSONBody: true}))
	jsonOnly.PATCH("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	jsonOnly.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	jsonOnly.PUT("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder = doRequest(jsonOnly, http.MethodPatch, "/submit", strings.NewReader("{}"), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("json suffix status = %d", recorder.Code)
	}
	recorder = doRequest(jsonOnly, http.MethodPost, "/submit", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bodyless status = %d", recorder.Code)
	}
	recorder = doRequest(jsonOnly, http.MethodPut, "/submit", strings.NewReader("text"), map[string]string{
		"Content-Type": "text/plain",
	})
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text status = %d", recorder.Code)
	}
}

func TestRecoveryWithoutPanicAndWrittenResponse(t *testing.T) {
	log, path := newTestLogger(t)
	engine := newTestEngine(Recovery(log))
	engine.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	engine.GET("/partial", func(c *gin.Context) {
		c.String(http.StatusOK, "partial")
		panic("after write")
	})

	if recorder := doRequest(engine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("normal response: %d %q", recorder.Code, recorder.Body.String())
	}
	recorder := doRequest(engine, http.MethodGet, "/partial", nil, nil)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "partial" {
		t.Fatalf("partial response: %d %q", recorder.Code, recorder.Body.String())
	}
	content := readTestLog(t, log, path)
	if !strings.Contains(content, "panic recovered") {
		t.Fatalf("expected panic log: %s", content)
	}
}

func TestRecoveryRepanicsAbortHandler(t *testing.T) {
	log, _ := newTestLogger(t)
	engine := newTestEngine(Recovery(log))
	engine.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })

	defer func() {
		recovered := recover()
		if recovered != http.ErrAbortHandler {
			t.Fatalf("recovered = %v, want ErrAbortHandler", recovered)
		}
	}()
	doRequest(engine, http.MethodGet, "/abort", nil, nil)
}

func TestRequestIDRejectsTooLongValue(t *testing.T) {
	engine := newTestEngine(RequestID())
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	value := strings.Repeat("a", 65)
	recorder := doRequest(engine, http.MethodGet, "/", nil, map[string]string{"X-Request-ID": value})
	got := recorder.Header().Get("X-Request-ID")
	if got == "" || got == value || len(got) > 64 {
		t.Fatalf("request id = %q", got)
	}
}

func TestCurrentConfig(t *testing.T) {
	current := GetConfig()
	if current == nil || !current.Enabled {
		t.Fatalf("unexpected config: %+v", current)
	}
}

func TestRateLimitKeyFunctions(t *testing.T) {
	anonymous, _ := gin.CreateTestContext(httptest.NewRecorder())
	anonymous.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := UserKey(anonymous); !strings.HasPrefix(got, "ip:") {
		t.Fatalf("anonymous user key = %q", got)
	}
	if got := TenantKey(anonymous); !strings.HasPrefix(got, "ip:") {
		t.Fatalf("anonymous tenant key = %q", got)
	}

	user, _ := gin.CreateTestContext(httptest.NewRecorder())
	user.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	setIdentity(user, &auth.Identity{UserID: "u1"})
	if got := UserKey(user); got != "user:u1" {
		t.Fatalf("user key = %q", got)
	}
	if got := TenantKey(user); !strings.HasPrefix(got, "ip:") {
		t.Fatalf("tenant fallback key = %q", got)
	}

	tenant, _ := gin.CreateTestContext(httptest.NewRecorder())
	tenant.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	setIdentity(tenant, &auth.Identity{UserID: "u1", TenantID: "t1"})
	if got := TenantKey(tenant); got != "tenant:t1" {
		t.Fatalf("tenant key = %q", got)
	}
}

func TestDefaultConfigAndCSRFCookieSkip(t *testing.T) {
	if DefaultConfig().Request.MaxBodySize <= 0 {
		t.Fatal("default max body size should be positive")
	}

	protector, err := csrf.New(csrf.Config{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("csrf.New: %v", err)
	}
	engine := newTestEngine(CSRF(protector))
	engine.OPTIONS("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(engine, http.MethodOptions, "/submit", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("OPTIONS status = %d", recorder.Code)
	}
}
