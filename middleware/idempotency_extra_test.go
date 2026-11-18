package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type failingStore struct {
	getErr    error
	setErr    error
	deleteErr error
	setCalls  int
	record    *Record
}

func (s *failingStore) Get(context.Context, string) (*Record, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.record, nil
}

func (s *failingStore) Set(context.Context, string, *Record, time.Duration) error {
	s.setCalls++
	return s.setErr
}

func (s *failingStore) Delete(context.Context, string) error { return s.deleteErr }

func TestIdempotencyWriterMethods(t *testing.T) {
	fake := newFakeResponseWriter()
	w := &idempotencyWriter{ResponseWriter: fake}
	if w.Status() != http.StatusOK {
		t.Fatalf("default status = %d", w.Status())
	}
	if w.Written() {
		t.Fatal("writer should not be written")
	}
	w.WriteHeaderNow()
	if !w.Written() || w.Status() != http.StatusOK {
		t.Fatalf("WriteHeaderNow state: %d %v", w.Status(), w.Written())
	}

	second := &idempotencyWriter{ResponseWriter: fake}
	if n, err := second.WriteString("body"); err != nil || n != 4 {
		t.Fatalf("WriteString = %d, %v", n, err)
	}
	if second.Status() != http.StatusOK || second.Size() != 4 || !second.Written() {
		t.Fatalf("state: %d %d %v", second.Status(), second.Size(), second.Written())
	}
	second.WriteHeader(http.StatusCreated)
	second.WriteHeader(http.StatusInternalServerError)
	if second.Status() != http.StatusOK {
		t.Fatalf("status = %d, want 200", second.Status())
	}
	headerOnly := &idempotencyWriter{ResponseWriter: fake}
	headerOnly.WriteHeader(http.StatusCreated)
	headerOnly.WriteHeader(http.StatusInternalServerError)
	if headerOnly.Status() != http.StatusCreated {
		t.Fatalf("status = %d, want 201", headerOnly.Status())
	}
	headerOnly.Flush()
	if _, _, err := second.Hijack(); err == nil {
		t.Fatal("hijack should be unsupported")
	}
	if second.CloseNotify() != fake.closeCh {
		t.Fatal("CloseNotify should delegate")
	}
	if second.Pusher() != nil {
		t.Fatal("Pusher should delegate")
	}
}

func TestIdempotencyStoreFailures(t *testing.T) {
	const key = "store-key"
	handler := func(c *gin.Context) { c.Status(http.StatusOK) }

	getFailure := &failingStore{getErr: errors.New("get failed")}
	engine := newTestEngine(Idempotency(getFailure, time.Minute))
	engine.POST("/x", handler)
	if recorder := doRequest(engine, http.MethodPost, "/x", nil, map[string]string{HeaderIdempotencyKey: key}); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("get failure status = %d", recorder.Code)
	}

	setFailure := &failingStore{setErr: errors.New("set failed")}
	engine = newTestEngine(Idempotency(setFailure, time.Minute))
	engine.POST("/x", handler)
	if recorder := doRequest(engine, http.MethodPost, "/x", nil, map[string]string{HeaderIdempotencyKey: key}); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("set failure status = %d", recorder.Code)
	}
}

func TestIdempotencyAbortedRequestClearsKey(t *testing.T) {
	store := NewMemoryStore()
	engine := newTestEngine(Idempotency(store, time.Minute), func(c *gin.Context) {
		c.Abort()
	})
	engine.POST("/abort", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodPost, "/abort", nil, map[string]string{HeaderIdempotencyKey: "abort-key"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	record, err := store.Get(context.Background(), "abort-key")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if record != nil {
		t.Fatal("aborted request should clear placeholder")
	}
}

func TestIdempotencyPanicClearsKey(t *testing.T) {
	log, _ := newTestLogger(t)
	store := NewMemoryStore()
	var calls int32
	engine := newTestEngine(Recovery(log), Idempotency(store, time.Minute))
	engine.POST("/panic", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		panic("boom")
	})

	headers := map[string]string{HeaderIdempotencyKey: "panic-key"}
	if recorder := doRequest(engine, http.MethodPost, "/panic", nil, headers); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d", recorder.Code)
	}
	if recorder := doRequest(engine, http.MethodPost, "/panic", nil, headers); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("second status = %d", recorder.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestIdempotencyPassThrough(t *testing.T) {
	var calls int32
	engine := newTestEngine(Idempotency(nil, time.Minute))
	engine.POST("/nil", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusOK)
	})
	if recorder := doRequest(engine, http.MethodPost, "/nil", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	store := NewMemoryStore()
	withoutKey := newTestEngine(Idempotency(store, 0))
	withoutKey.POST("/nokey", func(c *gin.Context) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusOK)
	})
	for i := 0; i < 2; i++ {
		if recorder := doRequest(withoutKey, http.MethodPost, "/nokey", nil, nil); recorder.Code != http.StatusOK {
			t.Fatalf("status = %d", recorder.Code)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("handler calls = %d, want 3", got)
	}
}

func TestMemoryStoreErrorPaths(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Get(ctx, "k"); err == nil {
		t.Fatal("get with canceled context should fail")
	}
	if err := store.Set(ctx, "k", &Record{Status: 200}, 0); err == nil {
		t.Fatal("set with canceled context should fail")
	}
	if err := store.Delete(ctx, "k"); err == nil {
		t.Fatal("delete with canceled context should fail")
	}
	if err := store.Set(context.Background(), "k", nil, 0); err == nil {
		t.Fatal("set nil record should fail")
	}
	if err := store.Set(context.Background(), "k", &Record{Status: 200, Body: []byte("ok")}, 0); err != nil {
		t.Fatalf("set without ttl: %v", err)
	}
	record, err := store.Get(context.Background(), "k")
	if err != nil || record == nil || record.Status != 200 {
		t.Fatalf("get = %+v, %v", record, err)
	}
}

func TestIdempotencyHelpers(t *testing.T) {
	if contextErr(nil) != nil {
		t.Fatal("nil context should be accepted")
	}
	if contextErr(context.Background()) != nil {
		t.Fatal("background context should be accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if contextErr(ctx) == nil {
		t.Fatal("canceled context should fail")
	}
	if cloneRecord(nil) != nil {
		t.Fatal("cloneRecord(nil) should be nil")
	}
	if cloneHeader(nil) != nil {
		t.Fatal("cloneHeader(nil) should be nil")
	}

	fake := newFakeResponseWriter()
	writeRecord(fake, &Record{})
	if fake.status != http.StatusOK || fake.body.Len() != 0 {
		t.Fatalf("empty record: %d %q", fake.status, fake.body.String())
	}
	fake = newFakeResponseWriter()
	writeRecord(fake, &Record{Status: http.StatusCreated, Body: []byte("body")})
	if fake.status != http.StatusCreated || fake.body.String() != "body" {
		t.Fatalf("record: %d %q", fake.status, fake.body.String())
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		if !idempotentMethod(method) {
			t.Fatalf("%s should be idempotent", method)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodHead} {
		if idempotentMethod(method) {
			t.Fatalf("%s should not be idempotent", method)
		}
	}
}
