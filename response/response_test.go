package response

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
)

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func perform(e *gin.Engine, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	return recorder
}

func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, recorder.Body.String())
	}
	return body
}

func TestSuccessResponse(t *testing.T) {
	e := newTestEngine()
	e.Use(func(c *gin.Context) {
		c.Set(RequestIDKey, "rid-1")
		c.Next()
	})
	e.GET("/ok", func(c *gin.Context) {
		Success(c, gin.H{"name": "alice"})
	})

	recorder := perform(e, http.MethodGet, "/ok")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := decodeBody[Response[map[string]any]](t, recorder)
	if body.Code != apperrors.CodeOK || body.Message != "ok" || body.RequestID != "rid-1" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
	if body.Data["name"] != "alice" {
		t.Fatalf("unexpected data: %+v", body.Data)
	}
}

func TestFailMapsAppError(t *testing.T) {
	e := newTestEngine()
	e.GET("/missing", func(c *gin.Context) {
		Fail(c, fmt.Errorf("service: %w", apperrors.NotFound("user not found")))
	})

	recorder := perform(e, http.MethodGet, "/missing")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	body := decodeBody[Response[any]](t, recorder)
	if body.Code != apperrors.CodeNotFound || body.Message != "user not found" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
}

func TestFailHidesInternalError(t *testing.T) {
	e := newTestEngine()
	e.GET("/internal", func(c *gin.Context) {
		Fail(c, stderrors.New("sql: table users does not exist"))
	})

	recorder := perform(e, http.MethodGet, "/internal")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" || strings.Contains(body, "table users") {
		t.Fatalf("internal details leaked: %s", body)
	}
	body := decodeBody[Response[any]](t, recorder)
	if body.Code != apperrors.CodeInternal {
		t.Fatalf("code = %d", body.Code)
	}
}

func TestNoContent(t *testing.T) {
	e := newTestEngine()
	e.DELETE("/resource", func(c *gin.Context) {
		NoContent(c)
	})

	recorder := perform(e, http.MethodDelete, "/resource")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %q", recorder.Body.String())
	}
}

func TestFailWith(t *testing.T) {
	e := newTestEngine()
	e.GET("/teapot", func(c *gin.Context) {
		FailWith(c, http.StatusTeapot, 41800, "short and stout")
	})

	recorder := perform(e, http.MethodGet, "/teapot")
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := decodeBody[Response[any]](t, recorder)
	if body.Code != 41800 || body.Message != "short and stout" {
		t.Fatalf("unexpected envelope: %+v", body)
	}
}

func TestNewPageInitializesEmptyList(t *testing.T) {
	page := NewPage[string](nil, 0, 1, 20)
	if page.List == nil {
		t.Fatal("expected empty list instead of nil")
	}
	if len(page.List) != 0 || page.Total != 0 || page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestRequestIDSkipsInvalidHeader(t *testing.T) {
	e := newTestEngine()
	e.Use(func(c *gin.Context) {
		c.Set(RequestIDKey, "from-context")
		c.Next()
	})
	e.GET("/rid", func(c *gin.Context) {
		Success(c, RequestID(c))
	})

	recorder := perform(e, http.MethodGet, "/rid")
	body := decodeBody[Response[string]](t, recorder)
	if body.Data != "from-context" {
		t.Fatalf("request id = %q", body.Data)
	}
}
