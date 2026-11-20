package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCORSEngine(t *testing.T, cfg CORSConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	serverCfg := DefaultConfig()
	serverCfg.CORS = cfg
	setupMiddleware(engine, serverCfg)
	engine.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	return engine
}

func corsRequest(engine *gin.Engine, method, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/ping", nil)
	req.Host = "api.example.com"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func TestCORSDisabledByDefault(t *testing.T) {
	engine := newCORSEngine(t, DefaultConfig().CORS)
	if recorder := corsRequest(engine, http.MethodGet, "https://app.example.com"); recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("cors header must be empty when disabled")
	}
}

func TestCORSAllowedOriginAndPreflight(t *testing.T) {
	cfg := DefaultConfig().CORS
	cfg.Enabled = true
	cfg.AllowedOrigins = []string{"https://app.example.com"}
	engine := newCORSEngine(t, cfg)

	normal := corsRequest(engine, http.MethodGet, "https://app.example.com")
	if normal.Code != http.StatusOK {
		t.Fatalf("status = %d", normal.Code)
	}
	if got := normal.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow origin = %q", got)
	}

	preflight := corsRequest(engine, http.MethodOptions, "https://app.example.com")
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflight.Code)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("preflight missing allow methods")
	}

	if denied := corsRequest(engine, http.MethodGet, "https://evil.example.com"); denied.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d, want 403", denied.Code)
	}
}

func TestCORSAllowAllOrigins(t *testing.T) {
	cfg := DefaultConfig().CORS
	cfg.Enabled = true
	cfg.AllowedOrigins = []string{"*"}
	engine := newCORSEngine(t, cfg)

	if got := corsRequest(engine, http.MethodGet, "https://any.example.com").Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow origin = %q", got)
	}
}

func TestCORSConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  CORSConfig
		ok   bool
	}{
		{"disabled", CORSConfig{}, true},
		{"missing origins", CORSConfig{Enabled: true}, false},
		{"empty origin", CORSConfig{Enabled: true, AllowedOrigins: []string{" "}}, false},
		{"missing scheme", CORSConfig{Enabled: true, AllowedOrigins: []string{"app.example.com"}}, false},
		{"wildcard origin", CORSConfig{Enabled: true, AllowedOrigins: []string{"https://*.example.com"}}, false},
		{"wildcard with credentials", CORSConfig{Enabled: true, AllowedOrigins: []string{"*"}, AllowCredentials: true}, false},
		{"exact origin", CORSConfig{Enabled: true, AllowedOrigins: []string{"https://app.example.com"}}, true},
		{"wildcard without credentials", CORSConfig{Enabled: true, AllowedOrigins: []string{"*"}}, true},
		{"negative max age", CORSConfig{Enabled: true, AllowedOrigins: []string{"https://app.example.com"}, MaxAge: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
