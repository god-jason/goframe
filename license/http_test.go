package license

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type httpEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func TestStatusAndFeatureMiddleware(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	manager := newTestManager(t, now, IssueOptions{
		ID:       "L-http",
		Product:  "goframe",
		IssuedAt: now,
		Features: []string{"sso"},
	}, nil)

	global.manager = manager
	t.Cleanup(func() {
		global.manager = nil
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/license", statusHandler())
	engine.GET("/sso", RequireFeature("sso"), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	engine.GET("/billing", RequireFeature("billing"), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/license", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d", recorder.Code)
	}
	var response httpEnvelope[Snapshot]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Code != 0 || response.Data.Status != StatusValid || !response.Data.Valid {
		t.Fatalf("response = %+v", response)
	}

	if code := getStatus(engine, "/sso"); code != http.StatusOK {
		t.Fatalf("sso status = %d", code)
	}
	if code := getStatus(engine, "/billing"); code != http.StatusForbidden {
		t.Fatalf("billing status = %d", code)
	}
}

func TestMiddlewareWithoutManager(t *testing.T) {
	global.manager = nil

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/license", statusHandler())
	engine.GET("/sso", RequireFeature("sso"), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	if code := getStatus(engine, "/license"); code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d", code)
	}
	if code := getStatus(engine, "/sso"); code != http.StatusForbidden {
		t.Fatalf("feature status = %d", code)
	}
}

func getStatus(engine *gin.Engine, path string) int {
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code
}
