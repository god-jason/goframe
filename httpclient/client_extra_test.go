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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type errReadCloser struct{ err error }

func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r errReadCloser) Close() error             { return nil }

func TestStateString(t *testing.T) {
	cases := map[State]string{
		StateClosed:   "closed",
		StateOpen:     "open",
		StateHalfOpen: "half-open",
		State(99):     "unknown",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Fatalf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}

func TestOptionsDefaults(t *testing.T) {
	client := New(Options{
		MaxRetries:       -1,
		RetryWaitMin:     -time.Second,
		RetryWaitMax:     -time.Second,
		MaxResponseBytes: -1,
	})
	if client.opts.MaxRetries != 0 || client.opts.Timeout != 30*time.Second {
		t.Fatalf("opts = %+v", client.opts)
	}
	if client.opts.RetryWaitMin != 100*time.Millisecond || client.opts.RetryWaitMax != 2*time.Second {
		t.Fatalf("opts = %+v", client.opts)
	}
	if client.opts.MaxResponseBytes != 10<<20 {
		t.Fatalf("max bytes = %d", client.opts.MaxResponseBytes)
	}

	client = New(Options{RetryWaitMin: time.Second, RetryWaitMax: 10 * time.Millisecond})
	if client.opts.RetryWaitMax != time.Second {
		t.Fatalf("RetryWaitMax = %v", client.opts.RetryWaitMax)
	}
}

func TestBreakerDefaults(t *testing.T) {
	breaker := NewBreaker(BreakerOptions{})
	if breaker.opts.FailureThreshold != 5 || breaker.opts.SuccessThreshold != 2 || breaker.opts.OpenTimeout != 30*time.Second {
		t.Fatalf("opts = %+v", breaker.opts)
	}
	for i := 0; i < 5; i++ {
		breaker.Failure()
	}
	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open", breaker.State())
	}
}

func TestDoNilAndCanceled(t *testing.T) {
	client := New(Options{MaxRetries: 1, RetryWaitMin: time.Millisecond})
	if _, err := client.Do(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := client.Do(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDoNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := New(Options{}).Do(nil, req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
}

func TestDoTransportErrorRetriesThenSuccess(t *testing.T) {
	var calls int32
	client := New(Options{
		MaxRetries:   2,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: time.Millisecond,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if atomic.AddInt32(&calls, 1) < 3 {
				return nil, errors.New("network down")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
		}),
	})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDoTransportErrorExhausted(t *testing.T) {
	client := New(Options{
		MaxRetries:   1,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: time.Millisecond,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("always down")
		}),
	})
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := client.Do(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoRetryWaitContextExpires(t *testing.T) {
	client := New(Options{
		MaxRetries:   2,
		RetryWaitMin: 200 * time.Millisecond,
		RetryWaitMax: 200 * time.Millisecond,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("down")
		}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := client.Do(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "retry wait") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoContextCanceledDuringRequest(t *testing.T) {
	client := New(Options{
		MaxRetries: 2,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := client.Do(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "do request") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoBuffersBodyWithoutGetBody(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPut, server.URL, nil)
	req.Body = io.NopCloser(strings.NewReader("payload"))
	req.GetBody = nil

	resp, err := New(Options{}).Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if received != "payload" {
		t.Fatalf("received = %q", received)
	}
}

func TestDoPrepareBodyReadError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "http://example.com", nil)
	req.Body = errReadCloser{err: errors.New("read failed")}
	req.GetBody = nil
	if _, err := New(Options{}).Do(context.Background(), req); err == nil {
		t.Fatal("expected body read error")
	}
}

func TestDoReplayBodyError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "http://example.com", nil)
	req.Body = io.NopCloser(strings.NewReader("x"))
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("replay failed") }
	client := New(Options{
		MaxRetries: 1,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}),
	})
	if _, err := client.Do(context.Background(), req); err == nil {
		t.Fatal("expected replay error")
	}
}

func TestBackoffAndWait(t *testing.T) {
	client := New(Options{RetryWaitMin: 10 * time.Millisecond, RetryWaitMax: 40 * time.Millisecond})
	for retry := 1; retry <= 10; retry++ {
		got := client.backoff(retry)
		if got <= 0 || got > 40*time.Millisecond {
			t.Fatalf("backoff(%d) = %v", retry, got)
		}
	}
	if err := wait(context.Background(), 0); err != nil {
		t.Fatalf("wait(0) = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait canceled = %v", err)
	}
}

func TestRecordFailure(t *testing.T) {
	client := New(Options{})
	// nil 熔断器不应 panic。
	client.recordFailure(nil, errors.New("boom"))
	client.recordFailure(nil, nil)

	breaker := NewBreaker(BreakerOptions{FailureThreshold: 1})
	client.recordFailure(breaker, context.Canceled)
	if breaker.State() != StateClosed {
		t.Fatal("canceled errors must not trip the breaker")
	}
	client.recordFailure(breaker, context.DeadlineExceeded)
	if breaker.State() != StateClosed {
		t.Fatal("deadline errors must not trip the breaker")
	}
	client.recordFailure(breaker, errors.New("boom"))
	if breaker.State() != StateOpen {
		t.Fatal("transport error should trip the breaker")
	}
}

func TestBreakerSuccessInClosedState(t *testing.T) {
	breaker := NewBreaker(BreakerOptions{FailureThreshold: 2})
	breaker.Failure()
	breaker.Success()
	breaker.Failure()
	if breaker.State() != StateClosed {
		t.Fatal("success should reset failure counter")
	}
}

func TestAPIErrorEmptyBody(t *testing.T) {
	err := &APIError{Status: 500}
	if err.Error() != "httpclient: unexpected status 500" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestGetJSONInvalidURL(t *testing.T) {
	if err := New(Options{}).GetJSON(context.Background(), "://bad", nil); err == nil {
		t.Fatal("expected create request error")
	}
}

func TestGetJSONNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	var out map[string]bool
	if err := New(Options{}).GetJSON(nil, server.URL, &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !out["ok"] {
		t.Fatalf("out = %+v", out)
	}
}

func TestPostJSONErrors(t *testing.T) {
	if err := New(Options{}).PostJSON(context.Background(), "http://example.com", make(chan int), nil); err == nil {
		t.Fatal("expected marshal error")
	}
	if err := New(Options{}).PostJSON(context.Background(), "://bad", nil, nil); err == nil {
		t.Fatal("expected create request error")
	}
}

func TestJSONDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-json")
	}))
	defer server.Close()
	var out map[string]any
	if err := New(Options{}).GetJSON(context.Background(), server.URL, &out); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestJSONEmptyBodyAndNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/no-content" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var out map[string]any
	if err := New(Options{}).GetJSON(context.Background(), server.URL, &out); err != nil {
		t.Fatalf("empty body: %v", err)
	}
	if err := New(Options{}).GetJSON(context.Background(), server.URL+"/no-content", &out); err != nil {
		t.Fatalf("no content: %v", err)
	}
	if err := New(Options{}).GetJSON(context.Background(), server.URL, nil); err != nil {
		t.Fatalf("nil out: %v", err)
	}
}

func TestJSONErrorResponseReadFailure(t *testing.T) {
	client := New(Options{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       errReadCloser{err: errors.New("read failed")},
				Header:     http.Header{},
			}, nil
		}),
	})
	if err := client.GetJSON(context.Background(), "http://example.com", nil); err == nil {
		t.Fatal("expected read error response error")
	}
}

func TestJSONDoError(t *testing.T) {
	client := New(Options{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("down")
		}),
	})
	if err := client.GetJSON(context.Background(), "http://example.com", nil); err == nil {
		t.Fatal("expected request error")
	}
}
