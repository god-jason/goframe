package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestIdempotencyReplaysCachedResponse(t *testing.T) {
	var calls int32
	engine := newTestEngine(Idempotency(NewMemoryStore(), time.Minute))
	engine.POST("/orders", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Header("X-Order", "42")
		c.JSON(http.StatusCreated, gin.H{"id": 42})
	})

	headers := map[string]string{HeaderIdempotencyKey: "create-order-1"}
	first := doRequest(engine, http.MethodPost, "/orders", nil, headers)
	second := doRequest(engine, http.MethodPost, "/orders", nil, headers)

	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("status = %d/%d, want 201", first.Code, second.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replayed body = %q, want %q", second.Body.String(), first.Body.String())
	}
	if second.Header().Get("X-Order") != "42" {
		t.Fatalf("replayed header missing: %q", second.Header().Get("X-Order"))
	}
}

func TestIdempotencyConcurrentRequestReturnsConflict(t *testing.T) {
	engine := newTestEngine(Idempotency(NewMemoryStore(), time.Minute))
	started := make(chan struct{})
	release := make(chan struct{})
	engine.POST("/slow", func(c *gin.Context) {
		close(started)
		<-release
		c.String(http.StatusOK, "done")
	})

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- doRequest(engine, http.MethodPost, "/slow", nil, map[string]string{HeaderIdempotencyKey: "same-key"})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}

	second := doRequest(engine, http.MethodPost, "/slow", nil, map[string]string{HeaderIdempotencyKey: "same-key"})
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409", second.Code)
	}

	close(release)
	first := <-firstDone
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}
}

func TestIdempotencyRejectsTooLongKey(t *testing.T) {
	var calls int32
	engine := newTestEngine(Idempotency(NewMemoryStore(), time.Minute))
	engine.POST("/orders", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusOK)
	})

	recorder := doRequest(engine, http.MethodPost, "/orders", nil, map[string]string{
		HeaderIdempotencyKey: strings.Repeat("k", maxIdempotencyKeyLength+1),
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("handler should not be invoked for invalid key")
	}
}

func TestIdempotencyIgnoresGet(t *testing.T) {
	var calls int32
	engine := newTestEngine(Idempotency(NewMemoryStore(), time.Minute))
	engine.GET("/orders", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusOK)
	})

	headers := map[string]string{HeaderIdempotencyKey: "get-key"}
	doRequest(engine, http.MethodGet, "/orders", nil, headers)
	doRequest(engine, http.MethodGet, "/orders", nil, headers)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestIdempotencyDoesNotCacheServerError(t *testing.T) {
	var calls int32
	engine := newTestEngine(Idempotency(NewMemoryStore(), time.Minute))
	engine.POST("/fail", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusInternalServerError)
	})

	headers := map[string]string{HeaderIdempotencyKey: "fail-key"}
	doRequest(engine, http.MethodPost, "/fail", nil, headers)
	doRequest(engine, http.MethodPost, "/fail", nil, headers)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestMemoryStoreExpiry(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "k", &Record{Status: 200, Body: []byte("ok")}, 10*time.Millisecond); err != nil {
		t.Fatalf("set: %v", err)
	}
	record, err := store.Get(ctx, "k")
	if err != nil || record == nil {
		t.Fatalf("get before expiry = %v, %v", record, err)
	}
	time.Sleep(15 * time.Millisecond)
	record, err = store.Get(ctx, "k")
	if err != nil {
		t.Fatalf("get after expiry: %v", err)
	}
	if record != nil {
		t.Fatal("record should expire")
	}
}
