package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(opts Options) *Client {
	return New(opts)
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := newTestClient(Options{
		MaxRetries:   3,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 5 * time.Millisecond,
	})
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDoNoRetryOn4xx(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(Options{MaxRetries: 3, RetryWaitMin: time.Millisecond, RetryWaitMax: time.Millisecond})
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestDoNonIdempotentNotRetried(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := newTestClient(Options{MaxRetries: 3, RetryWaitMin: time.Millisecond, RetryWaitMax: time.Millisecond})
	req, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("payload"))

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestDoReplaysRequestBodyOnRetry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("body = %q, want payload", body)
		}
		if atomic.AddInt32(&attempts, 1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(Options{MaxRetries: 2, RetryWaitMin: time.Millisecond, RetryWaitMax: time.Millisecond})
	// PUT 是幂等方法，且请求体需要被缓冲后重放。
	req, _ := http.NewRequest(http.MethodPut, server.URL, strings.NewReader("payload"))

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestDoResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 128))
	}))
	defer server.Close()

	client := newTestClient(Options{MaxResponseBytes: 16, MaxRetries: 0})
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("expected error when response exceeds limit")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBreakerOpensAfterFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	breaker := NewBreaker(BreakerOptions{FailureThreshold: 1, OpenTimeout: time.Minute})
	client := newTestClient(Options{MaxRetries: 0, Breaker: breaker})
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("first do: %v", err)
	}
	resp.Body.Close()
	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open", breaker.State())
	}

	if _, err := client.Do(context.Background(), req); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
}
