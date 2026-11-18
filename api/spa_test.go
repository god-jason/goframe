package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
)

func newSPAEngine(t *testing.T) *gin.Engine {
	t.Helper()
	fsys := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>index</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
	}
	handler, err := NewSPAHandler(fsys, "/api/v1", SPAOptions{Index: "index.html", ImmutablePrefix: "assets"})
	if err != nil {
		t.Fatalf("NewSPAHandler: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.NoRoute(handler)
	return engine
}

func performSPA(engine *gin.Engine, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func TestSPAFallbackToIndex(t *testing.T) {
	engine := newSPAEngine(t)
	recorder := performSPA(engine, http.MethodGet, "/dashboard/users")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "index") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q", got)
	}
}

func TestSPAServesStaticFile(t *testing.T) {
	engine := newSPAEngine(t)
	recorder := performSPA(engine, http.MethodGet, "/assets/app.js")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "console.log") {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control = %q", got)
	}
}

func TestSPAUnknownAPIReturnsJSON404(t *testing.T) {
	engine := newSPAEngine(t)
	recorder := performSPA(engine, http.MethodGet, "/api/v1/unknown")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body Response[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != apperrors.CodeNotFound {
		t.Fatalf("code = %d", body.Code)
	}
}

func TestSPARejectsUnsupportedMethod(t *testing.T) {
	engine := newSPAEngine(t)
	recorder := performSPA(engine, http.MethodPost, "/dashboard")

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body Response[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != apperrors.CodeMethodNotAllowed {
		t.Fatalf("code = %d", body.Code)
	}
}

func TestSPARequiresIndexFile(t *testing.T) {
	if _, err := NewSPAHandler(fstest.MapFS{}, "/api/v1", SPAOptions{}); err == nil {
		t.Fatal("expected missing index error")
	}
}
