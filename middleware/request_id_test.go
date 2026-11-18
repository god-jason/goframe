package middleware

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

func TestRequestIDPreservesValidValue(t *testing.T) {
	engine := newTestEngine(RequestID())
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, api.RequestID(c)) })

	recorder := doRequest(engine, http.MethodGet, "/", nil, map[string]string{api.HeaderRequestID: "req-123.abc"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(api.HeaderRequestID); got != "req-123.abc" {
		t.Fatalf("response request id = %q", got)
	}
	if recorder.Body.String() != "req-123.abc" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestRequestIDReplacesInvalidValue(t *testing.T) {
	engine := newTestEngine(RequestID())
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, api.RequestID(c)) })

	recorder := doRequest(engine, http.MethodGet, "/", nil, map[string]string{
		api.HeaderRequestID: "<script>alert(1)</script>",
	})
	got := recorder.Header().Get(api.HeaderRequestID)
	if got == "" || got == "<script>alert(1)</script>" {
		t.Fatalf("invalid request id was not replaced: %q", got)
	}
	if len(got) > 64 {
		t.Fatalf("generated request id too long: %q", got)
	}
	if recorder.Body.String() != got {
		t.Fatalf("body %q != header %q", recorder.Body.String(), got)
	}
}
