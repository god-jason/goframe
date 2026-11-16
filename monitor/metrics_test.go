package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsMiddlewareAndHandler(t *testing.T) {
	metrics := NewMetrics()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(metrics.Middleware())
	engine.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/x", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	if got := testutil.ToFloat64(metrics.requests.WithLabelValues(http.MethodGet, "/x", "200")); got != 1 {
		t.Fatalf("request counter = %v, want 1", got)
	}

	metricsEngine := gin.New()
	metricsEngine.GET("/metrics", metrics.Handler())
	metricsRecorder := httptest.NewRecorder()
	metricsEngine.ServeHTTP(metricsRecorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if metricsRecorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", metricsRecorder.Code)
	}
	body := metricsRecorder.Body.String()
	for _, expected := range []string{"goframe_http_requests_total", "goframe_http_request_duration_seconds"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics output does not contain %q", expected)
		}
	}
	if metrics.Registry() == nil {
		t.Fatal("expected registry")
	}
}
