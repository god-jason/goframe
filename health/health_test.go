package health

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/logger"
)

func newHealthEngine(checker *Checker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	api.Reset()
	checker.Register()
	engine := gin.New()
	// 单元测试直接验证处理器，跳过管理员守卫。
	if err := api.MountRoot(engine, api.WithGuard(api.Admin)); err != nil {
		panic(err)
	}
	return engine
}

// TestHealthRouteAccess 验证聚合检查仅管理员可访问，存活/就绪探针保持公开。
func TestHealthRouteAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api.Reset()
	defer api.Reset()

	New("v1").Register()
	engine := gin.New()
	if err := api.MountRoot(engine); err != nil {
		t.Fatalf("MountRoot: %v", err)
	}

	if code := perform(engine, "/health").Code; code != http.StatusUnauthorized {
		t.Fatalf("/health status = %d, want 401", code)
	}
	if code := perform(engine, "/health/live").Code; code != http.StatusOK {
		t.Fatalf("/health/live status = %d, want 200", code)
	}
	if code := perform(engine, "/health/ready").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("/health/ready status = %d, want 503", code)
	}
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

func TestCheckPanicIsIsolated(t *testing.T) {
	checker := New("v1",
		Check{
			Name:     "panic-check",
			Critical: true,
			Fn:       func(context.Context) error { panic("check boom") },
		},
		Check{
			Name:     "ok-check",
			Critical: false,
			Fn:       func(context.Context) error { return nil },
		},
	)
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if report.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", report.Status)
	}

	var foundPanic, foundOK bool
	for _, result := range report.Checks {
		switch result.Name {
		case "panic-check":
			foundPanic = true
			if result.Status != StatusFailed || !strings.Contains(result.Error, "panic") {
				t.Fatalf("unexpected panic result: %+v", result)
			}
		case "ok-check":
			foundOK = true
			if result.Status != StatusOK {
				t.Fatalf("unexpected ok result: %+v", result)
			}
		}
	}
	if !foundPanic || !foundOK {
		t.Fatalf("missing check results: %+v", report.Checks)
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

func TestSetLoggerAndTimeout(t *testing.T) {
	checker := New("v1")
	checker.SetLogger(logger.NewNop())
	checker.SetLogger(nil)
	checker.SetTimeout(0)
	checker.SetTimeout(50 * time.Millisecond)
	if checker.IsReady() {
		t.Fatal("new checker should not be ready")
	}
	checker.SetReady(true)
	if !checker.IsReady() {
		t.Fatal("checker should be ready")
	}
}

func TestReadinessSkipsNonCriticalChecks(t *testing.T) {
	checker := New("v1",
		Check{
			Name:     "cache",
			Critical: false,
			Fn:       func(context.Context) error { return stderrors.New("cache down") },
		},
		Check{
			Name:     "database",
			Critical: true,
			Fn:       func(context.Context) error { return nil },
		},
	)
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health/ready")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if len(report.Checks) != 1 || report.Checks[0].Name != "database" {
		t.Fatalf("readiness should only run critical checks: %+v", report.Checks)
	}
}

func TestCheckTimeout(t *testing.T) {
	checker := New("v1", Check{
		Name:     "slow",
		Critical: true,
		Fn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	checker.SetTimeout(20 * time.Millisecond)
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if len(report.Checks) != 1 || report.Checks[0].Status != StatusFailed {
		t.Fatalf("unexpected checks: %+v", report.Checks)
	}
}

func TestConcurrentChecks(t *testing.T) {
	checks := make([]Check, 0, 10)
	for i := 0; i < 10; i++ {
		checks = append(checks, Check{
			Name:     "check-" + strconv.Itoa(i),
			Critical: true,
			Fn: func(context.Context) error {
				time.Sleep(2 * time.Millisecond)
				return nil
			},
		})
	}
	checker := New("v1", checks...)
	checker.SetReady(true)
	engine := newHealthEngine(checker)

	recorder := perform(engine, "/health")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	report := decodeReport(t, recorder)
	if len(report.Checks) != 10 {
		t.Fatalf("checks = %d, want 10", len(report.Checks))
	}
	for _, result := range report.Checks {
		if result.Status != StatusOK {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
}
