package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGzipCompressesLargeResponse(t *testing.T) {
	engine := newTestEngine(Gzip(GzipMinSize(16)))
	body := strings.Repeat("hello world ", 100)
	engine.GET("/big", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString(body)
	})

	recorder := doRequest(engine, http.MethodGet, "/big", nil, nil)
	if got := recorder.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content encoding = %q, want gzip", got)
	}
	if got := recorder.Header().Get("Content-Length"); got != "" {
		t.Fatalf("content length = %q, want empty", got)
	}
	if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("vary = %q", got)
	}

	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if string(data) != body {
		t.Fatalf("body mismatch: got %d bytes", len(data))
	}
}

func TestGzipSkipsSmallResponse(t *testing.T) {
	engine := newTestEngine(Gzip(GzipMinSize(1024)))
	engine.GET("/small", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString("tiny")
	})

	recorder := doRequest(engine, http.MethodGet, "/small", nil, nil)
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q, want empty", got)
	}
	if recorder.Body.String() != "tiny" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestGzipSkipsExistingEncoding(t *testing.T) {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Header("Content-Encoding", "br")
	}, Gzip())
	engine.GET("/encoded", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString(strings.Repeat("data", 100))
	})

	recorder := doRequest(engine, http.MethodGet, "/encoded", nil, nil)
	if got := recorder.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("content encoding = %q, want br", got)
	}
	if recorder.Body.String() != strings.Repeat("data", 100) {
		t.Fatal("body should not be compressed")
	}
}

func TestGzipSkipsSSE(t *testing.T) {
	engine := newTestEngine(Gzip(GzipMinSize(1)))
	engine.GET("/events", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("data: hello\n\n")
	})

	recorder := doRequest(engine, http.MethodGet, "/events", nil, map[string]string{"Accept": "text/event-stream"})
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q, want empty", got)
	}
	if recorder.Body.String() != "data: hello\n\n" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestGzipSkipsWebSocket(t *testing.T) {
	engine := newTestEngine(Gzip(GzipMinSize(1)))
	engine.GET("/ws", func(c *gin.Context) {
		_, _ = c.Writer.WriteString("upgrade")
	})

	recorder := doRequest(engine, http.MethodGet, "/ws", nil, map[string]string{
		"Upgrade":    "websocket",
		"Connection": "Upgrade",
	})
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content encoding = %q, want empty", got)
	}
}
