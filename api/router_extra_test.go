package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestRouterAccessorsAndNoRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := New(engine, Options{})

	if r.Engine() != engine {
		t.Fatal("Engine() must return the underlying engine")
	}
	if r.API() == nil {
		t.Fatal("API() must not be nil")
	}
	if r.Group("").BasePath() != "/" {
		t.Fatalf("empty group base path = %q", r.Group("").BasePath())
	}

	r.NoRoute(func(c *gin.Context) { c.String(http.StatusNotFound, "fallback") })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound || recorder.Body.String() != "fallback" {
		t.Fatalf("unexpected no-route response: %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestSPAConfigDefault(t *testing.T) {
	cfg := SPA()
	if cfg == nil || cfg.Index != "index.html" {
		t.Fatalf("unexpected spa config: %+v", cfg)
	}
	if got := DefaultSPAConfig(); got.Mode != "embed" || got.Enabled {
		t.Fatalf("unexpected default spa config: %+v", got)
	}
}

func TestSPADefaultIndexAndEmptyAPIPath(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>root</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	handler, err := NewSPAHandler(fsys, "", SPAOptions{})
	if err != nil {
		t.Fatalf("NewSPAHandler: %v", err)
	}
	g := gin.New()
	g.NoRoute(handler)

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if !strings.Contains(recorder.Body.String(), "root") {
		t.Fatalf("body = %q", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/app.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d", recorder.Code)
	}

	// 路径穿越被 Clean 限制在根目录内，不存在时回退 index。
	recorder = httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))
	if !strings.Contains(recorder.Body.String(), "root") {
		t.Fatalf("traversal body = %q", recorder.Body.String())
	}
}

func TestSPAExactAPIPathReturns404(t *testing.T) {
	fsys := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("index")}}
	handler, err := NewSPAHandler(fsys, "/api", SPAOptions{})
	if err != nil {
		t.Fatalf("NewSPAHandler: %v", err)
	}
	g := gin.New()
	g.NoRoute(handler)

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestDirectorySPA(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>dir</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logo.txt"), []byte("logo"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	handler, err := DirectorySPA(dir, "/api", SPAOptions{})
	if err != nil {
		t.Fatalf("DirectorySPA: %v", err)
	}
	g := gin.New()
	g.NoRoute(handler)

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/logo.txt", nil))
	if recorder.Body.String() != "logo" {
		t.Fatalf("asset body = %q", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/deep/link", nil))
	if !strings.Contains(recorder.Body.String(), "dir") {
		t.Fatalf("fallback body = %q", recorder.Body.String())
	}

	if _, err := DirectorySPA(t.TempDir(), "/api", SPAOptions{}); err == nil {
		t.Fatal("expected missing index error")
	}
}
