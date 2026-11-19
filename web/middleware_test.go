package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestSetupMiddlewareRecoversPanic(t *testing.T) {
	engine := newMiddlewareEngine()
	setupMiddleware(engine, DefaultConfig())
	engine.GET("/panic", func(c *gin.Context) { panic("boom") })

	recorder := doMiddlewareRequest(engine, http.MethodGet, "/panic", nil, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}

func TestDefaultConfigRequestLimits(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Request.MaxBodySize <= 0 {
		t.Fatal("default max body size should be positive")
	}
	if len(cfg.Request.AllowedMethods) == 0 {
		t.Fatal("default allowed methods should not be empty")
	}
}

func TestMethodFilterBlocksTraceAndUnlistedMethods(t *testing.T) {
	engine := newMiddlewareEngine(methodFilter([]string{http.MethodGet}))
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doMiddlewareRequest(engine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", recorder.Code)
	}
	for _, method := range []string{http.MethodTrace, http.MethodPut} {
		recorder := doMiddlewareRequest(engine, method, "/ok", nil, nil)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, recorder.Code)
		}
		if recorder.Header().Get("Allow") == "" {
			t.Fatalf("%s response missing Allow header", method)
		}
	}
}

func TestMethodFilterDefaultMethods(t *testing.T) {
	engine := newMiddlewareEngine(methodFilter(nil))
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doMiddlewareRequest(engine, http.MethodGet, "/ok", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d", recorder.Code)
	}
	if recorder := doMiddlewareRequest(engine, http.MethodTrace, "/ok", nil, nil); recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("TRACE status = %d", recorder.Code)
	}
}

func TestRequestLimits(t *testing.T) {
	cfg := RequestConfig{
		MaxBodySize:     10,
		MaxQueryLength:  8,
		RequireJSONBody: true,
	}
	engine := newMiddlewareEngine(requestLimits(cfg))
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
			recorder := doMiddlewareRequest(engine, http.MethodPost, tc.target, strings.NewReader(tc.body), map[string]string{
				"Content-Type": tc.contentType,
			})
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestRequestLimitsDisabledAndJSONSuffix(t *testing.T) {
	unlimited := newMiddlewareEngine(requestLimits(RequestConfig{}))
	unlimited.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := doMiddlewareRequest(unlimited, http.MethodPost, "/submit", strings.NewReader("a=1"), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("unlimited status = %d", recorder.Code)
	}

	jsonOnly := newMiddlewareEngine(requestLimits(RequestConfig{RequireJSONBody: true}))
	jsonOnly.PATCH("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	jsonOnly.POST("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	jsonOnly.PUT("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder = doMiddlewareRequest(jsonOnly, http.MethodPatch, "/submit", strings.NewReader("{}"), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("json suffix status = %d", recorder.Code)
	}
	recorder = doMiddlewareRequest(jsonOnly, http.MethodPost, "/submit", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bodyless status = %d", recorder.Code)
	}
	recorder = doMiddlewareRequest(jsonOnly, http.MethodPut, "/submit", strings.NewReader("text"), map[string]string{
		"Content-Type": "text/plain",
	})
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text status = %d", recorder.Code)
	}
}
