package ratelimit

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
)

func newMiddlewareEngine(middlewares ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewares...)
	return engine
}

func doMiddlewareRequest(engine *gin.Engine, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func putIdentity(c *gin.Context, identity *auth.Identity) {
	c.Set(auth.IdentityKey, identity)
	c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), identity))
}

func TestRateLimitMiddleware(t *testing.T) {
	limiter := NewKeyedLimiter(0.001, 1)
	defer limiter.Close()

	engine := newMiddlewareEngine(Middleware(limiter, func(*gin.Context) string { return "fixed" }, false))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doMiddlewareRequest(engine, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("first status = %d", recorder.Code)
	}
	recorder := doMiddlewareRequest(engine, http.MethodGet, "/", nil, nil)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestRateLimitNilLimiter(t *testing.T) {
	engine := newMiddlewareEngine(Middleware(nil, nil, false))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doMiddlewareRequest(engine, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string) (bool, error) {
	return false, stderrors.New("limiter down")
}

func TestRateLimitFailureMode(t *testing.T) {
	open := newMiddlewareEngine(Middleware(failingLimiter{}, nil, true))
	open.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doMiddlewareRequest(open, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("fail-open status = %d", recorder.Code)
	}

	closed := newMiddlewareEngine(Middleware(failingLimiter{}, nil, false))
	closed.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doMiddlewareRequest(closed, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d, want 503", recorder.Code)
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
	putIdentity(user, &auth.Identity{UserID: "u1"})
	if got := UserKey(user); got != "user:u1" {
		t.Fatalf("user key = %q", got)
	}
	if got := TenantKey(user); !strings.HasPrefix(got, "ip:") {
		t.Fatalf("tenant fallback key = %q", got)
	}

	tenant, _ := gin.CreateTestContext(httptest.NewRecorder())
	tenant.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	putIdentity(tenant, &auth.Identity{UserID: "u1", TenantID: "t1"})
	if got := TenantKey(tenant); got != "tenant:t1" {
		t.Fatalf("tenant key = %q", got)
	}
}
