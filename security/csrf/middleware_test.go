package csrf

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

func TestCSRFMiddleware(t *testing.T) {
	protector, err := New(Config{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("csrf.New: %v", err)
	}

	engine := newMiddlewareEngine(Middleware(protector))
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

	tokenRecorder := doMiddlewareRequest(engine, http.MethodGet, "/token", nil, nil)
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
	if recorder := doMiddlewareRequest(engine, http.MethodGet, "/safe", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("safe method status = %d", recorder.Code)
	}
}

func TestCSRFNilProtector(t *testing.T) {
	engine := newMiddlewareEngine(Middleware(nil))
	engine.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doMiddlewareRequest(engine, http.MethodPost, "/submit", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestCSRFMethodsWithoutCookiePass(t *testing.T) {
	protector, err := New(Config{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("csrf.New: %v", err)
	}
	engine := newMiddlewareEngine(Middleware(protector))
	engine.OPTIONS("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doMiddlewareRequest(engine, http.MethodOptions, "/submit", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("OPTIONS status = %d", recorder.Code)
	}
}
