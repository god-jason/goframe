package middleware

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/logger"
)

func newTestEngine(middlewares ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewares...)
	return engine
}

func doRequest(engine *gin.Engine, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func newTestLogger(t *testing.T) (logger.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.log")
	log, err := logger.New(logger.Options{Level: "debug", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log, path
}

func readTestLog(t *testing.T, log logger.Logger, path string) string {
	t.Helper()
	if err := log.Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}
