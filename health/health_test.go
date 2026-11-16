package health

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newHealthEngine(checker *Checker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	checker.Register(engine)
	return engine
}

func perform(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

func decodeReport(t *testing.T, recorder *httptest.ResponseRecorder) Report {
	t.Helper()
	var report Report
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (body=%s)", err, recorder.Body.String())
	}
	return report
}

func TestLivenessAndReadiness(t *testing.T) {
	checker := New("v1")
	engine := newHealthEngine(checker)

	live := perform(engine, "/health/live")
	if live.Code != http.StatusOK {
		t.Fatalf("live status = %d", live.Code)
	}
	if report := decodeReport(t, live); report.Status != StatusOK || report.Version != "v1" {
		t.Fatalf("unexpected live report: %+v", report)
	}

	ready := perform(engine, "/health/ready")
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", ready.Code)
	}
	if report := decodeReport(t, ready); report.Status != StatusUnavailable {
		t.Fatalf("unexpected ready report: %+v", report)
	}

	checker.SetReady(true)
	ready = perform(engine, "/health/ready")
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", ready.Code)
	}
}

func TestCriticalCheckFailure(t *testing.T) {
	checker := New("v1", Check{
		Name:     "database",
		Critical: true,
		Fn:       func(context.Context) error { return stderrors.New("connection refused") },
	})
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	for _, path := range []string{"/health", "/health/ready"} {
		recorder := perform(engine, path)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, recorder.Code)
		}
		report := decodeReport(t, recorder)
		if report.Status != StatusFailed {
			t.Fatalf("%s status field = %q", path, report.Status)
		}
		if len(report.Checks) != 1 || report.Checks[0].Error == "" {
			t.Fatalf("%s checks = %+v", path, report.Checks)
		}
	}
}

func TestNonCriticalCheckFailure(t *testing.T) {
	checker := New("v1", Check{
		Name:     "cache",
		Critical: false,
		Fn:       func(context.Context) error { return stderrors.New("cache down") },
	})
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if report.Status != StatusOK {
		t.Fatalf("status = %q, want ok", report.Status)
	}
	if len(report.Checks) != 1 || report.Checks[0].Status != StatusFailed {
		t.Fatalf("unexpected checks: %+v", report.Checks)
	}
}

func TestCheckWithNilFuncAndDuplicateAdd(t *testing.T) {
	checker := New("v1", Check{Name: "noop", Critical: true})
	if err := checker.Add(Check{Name: "noop"}); err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if len(report.Checks) != 1 {
		t.Fatalf("expected duplicate check to be ignored, got %+v", report.Checks)
	}
}

func TestDatabaseAndCacheCheckHelpers(t *testing.T) {
	dbCheck := DatabaseCheck("db", stubPinger{err: nil})
	if !dbCheck.Critical || dbCheck.Name != "db" {
		t.Fatalf("unexpected db check: %+v", dbCheck)
	}
	cacheCheck := CacheCheck("cache", stubPinger{err: stderrors.New("down")})
	if !cacheCheck.Critical || cacheCheck.Name != "cache" {
		t.Fatalf("unexpected cache check: %+v", cacheCheck)
	}
	if err := cacheCheck.Fn(context.Background()); err == nil {
		t.Fatal("expected pinger error")
	}

	funcCheck := FuncCheck("custom", false, func(context.Context) error { return nil })
	if funcCheck.Critical {
		t.Fatal("expected non-critical check")
	}
}

type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }
