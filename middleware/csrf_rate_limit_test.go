package middleware

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/security/csrf"
	"github.com/god-jason/goframe/security/ratelimit"
)

func TestCSRFMiddleware(t *testing.T) {
	protector, err := csrf.New(config.CSRFConfig{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("csrf.New: %v", err)
	}

	engine := newTestEngine(CSRF(protector))
	engine.GET("/token", func(c *gin.Context) {
		token, err := protector.Token(c)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.String(http.StatusOK, token)
	})
	engine.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/safe", func(c *gin.Context) { c.Status(http.StatusOK) })

	tokenRecorder := doRequest(engine, http.MethodGet, "/token", nil, nil)
	token := tokenRecorder.Body.String()
	cookies := tokenRecorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected csrf cookie")
	}
	cookie := cookies[0]

	submit := func(headers map[string]string, withCookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/submit", nil)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		if withCookie {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	if recorder := submit(map[string]string{protector.HeaderName(): token}, true); recorder.Code != http.StatusOK {
		t.Fatalf("valid csrf status = %d", recorder.Code)
	}
	if recorder := submit(nil, true); recorder.Code != http.StatusForbidden {
		t.Fatalf("missing header status = %d, want 403", recorder.Code)
	}
	if recorder := submit(map[string]string{protector.HeaderName(): token}, false); recorder.Code != http.StatusOK {
		t.Fatalf("request without csrf cookie should pass, got %d", recorder.Code)
	}
	bearer := submit(map[string]string{"Authorization": "Bearer token"}, true)
	if bearer.Code != http.StatusOK {
		t.Fatalf("bearer request status = %d", bearer.Code)
	}
	if recorder := doRequest(engine, http.MethodGet, "/safe", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("safe method status = %d", recorder.Code)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	limiter := ratelimit.NewKeyedLimiter(0.001, 1)
	defer limiter.Close()

	engine := newTestEngine(RateLimit(limiter, func(*gin.Context) string { return "fixed" }, false))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("first status = %d", recorder.Code)
	}
	recorder := doRequest(engine, http.MethodGet, "/", nil, nil)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestRateLimitNilLimiter(t *testing.T) {
	engine := newTestEngine(RateLimit(nil, nil, false))
	engine.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(engine, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string) (bool, error) {
	return false, stderrors.New("limiter down")
}

func TestRateLimitFailureMode(t *testing.T) {
	open := newTestEngine(RateLimit(failingLimiter{}, nil, true))
	open.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(open, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("fail-open status = %d", recorder.Code)
	}

	closed := newTestEngine(RateLimit(failingLimiter{}, nil, false))
	closed.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(closed, http.MethodGet, "/", nil, nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d, want 503", recorder.Code)
	}
}
