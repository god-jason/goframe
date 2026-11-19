package openapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

// mountTestEngine 把当前注册表中的框架根路径接口挂载到新引擎。
func mountTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	engine := gin.New()
	if err := api.MountRoot(engine); err != nil {
		t.Fatalf("mount root: %v", err)
	}
	return engine
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
	api.Reset()
	Register()
	engine := mountTestEngine(t)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when disabled", recorder.Code)
	}
}

func TestRegisterEnabled(t *testing.T) {
	api.Reset()
	register(Config{Enabled: true, Path: "/swagger"})
	engine := mountTestEngine(t)

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

func TestResolvePath(t *testing.T) {
	cases := map[string]string{
		"":             "/swagger",
		"swagger":      "/swagger",
		"/swagger/":    "/swagger",
		" /docs/ ":     "/docs",
		"/docs/api/v1": "/docs/api/v1",
		"/":            "/swagger",
	}
	for input, want := range cases {
		if got := resolvePath(input); got != want {
			t.Fatalf("resolvePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegisterEmptyPath(t *testing.T) {
	api.Reset()
	register(Config{Enabled: true})
	engine := mountTestEngine(t)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, want 301", recorder.Code)
	}
}
