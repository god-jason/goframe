package httpclient

import (
	"testing"
	"time"
)

func TestBreakerHalfOpenRecovers(t *testing.T) {
	breaker := NewBreaker(BreakerOptions{
		FailureThreshold: 1,
		OpenTimeout:      10 * time.Millisecond,
		SuccessThreshold: 2,
	})

	breaker.Failure()
	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("open breaker should reject requests before timeout")
	}

	time.Sleep(15 * time.Millisecond)
	if !breaker.Allow() {
		t.Fatal("breaker should allow probe after open timeout")
	}
	if breaker.State() != StateHalfOpen {
		t.Fatalf("state = %s, want half-open", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("half-open breaker should allow only one probe at a time")
	}

	breaker.Success()
	if breaker.State() != StateHalfOpen {
		t.Fatalf("state = %s, want half-open until success threshold", breaker.State())
	}
	if !breaker.Allow() {
		t.Fatal("half-open breaker should allow next probe after success")
	}
	breaker.Success()
	if breaker.State() != StateClosed {
		t.Fatalf("state = %s, want closed", breaker.State())
	}
}

func TestBreakerProbeFailureReopens(t *testing.T) {
	breaker := NewBreaker(BreakerOptions{
		FailureThreshold: 1,
		OpenTimeout:      time.Millisecond,
		SuccessThreshold: 1,
	})
	breaker.Failure()
	time.Sleep(5 * time.Millisecond)
	if !breaker.Allow() {
		t.Fatal("expected probe to be allowed")
	}
	breaker.Failure()
	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("reopened breaker should reject requests")
	}
}
