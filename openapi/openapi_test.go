package openapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Fatal("openapi should be disabled by default")
	}
	if cfg.Path != "/swagger" {
		t.Fatalf("path = %q, want /swagger", cfg.Path)
	}
	if GetConfig() == nil {
		t.Fatal("current config is nil")
	}
}

func TestRegisterDisabled(t *testing.T) {
	engine := newTestEngine()
	Register(engine)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when disabled", recorder.Code)
	}
}

func TestRegisterEnabled(t *testing.T) {
	engine := newTestEngine()
	register(engine, Config{Enabled: true, Path: "/swagger"})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, want 301", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/swagger/index.html" {
		t.Fatalf("location = %q", location)
	}

	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(strings.ToLower(recorder.Body.String()), "swagger") {
		t.Fatalf("index body does not look like swagger ui: %q", recorder.Body.String()[:min(recorder.Body.Len(), 120)])
	}
}

func TestHandlerNotEmpty(t *testing.T) {
	if Handler() == nil {
		t.Fatal("handler is nil")
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":             "/swagger",
		"swagger":      "/swagger",
		"/swagger/":    "/swagger",
		" /docs/ ":     "/docs",
		"/docs/api/v1": "/docs/api/v1",
		"/":            "/swagger",
	}
	for input, want := range cases {
		if got := normalizePath(input); got != want {
			t.Fatalf("normalizePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegisterNilEngine(t *testing.T) {
	register(nil, Config{Enabled: true, Path: "/swagger"})
}

func TestRegisterEmptyPath(t *testing.T) {
	engine := newTestEngine()
	register(engine, Config{Enabled: true})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, want 301", recorder.Code)
	}
}
