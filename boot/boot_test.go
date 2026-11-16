package boot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder 记录任务启动/释放顺序，便于断言依赖拓扑与逆序释放。
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) add(name string) {
	r.mu.Lock()
	r.order = append(r.order, name)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *recorder) task(name string, depends ...string) *Task {
	return &Task{
		Depends: depends,
		Startup: func(context.Context) error {
			r.add("start:" + name)
			return nil
		},
		Shutdown: func(context.Context) error {
			r.add("stop:" + name)
			return nil
		},
	}
}

func TestStartupAndShutdownOrder(t *testing.T) {
	rec := &recorder{}
	registry := NewRegistry()

	for _, item := range []struct {
		name    string
		depends []string
	}{
		{"database", nil},
		{"cache", []string{"database"}},
		{"broker", []string{"cache"}},
		{"mqtt", []string{"broker"}},
		{"modules", []string{"mqtt"}},
		{"http", []string{"modules"}},
	} {
		if err := registry.Register(item.name, rec.task(item.name, item.depends...)); err != nil {
			t.Fatalf("register %s: %v", item.name, err)
		}
	}

	if err := registry.Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	wantStart := []string{
		"start:database", "start:cache", "start:broker",
		"start:mqtt", "start:modules", "start:http",
	}
	if got := rec.snapshot(); strings.Join(got, ",") != strings.Join(wantStart, ",") {
		t.Fatalf("startup order = %v, want %v", got, wantStart)
	}

	if err := registry.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	wantStop := []string{
		"stop:http", "stop:modules", "stop:mqtt",
		"stop:broker", "stop:cache", "stop:database",
	}
	got := rec.snapshot()[len(wantStart):]
	if strings.Join(got, ",") != strings.Join(wantStop, ",") {
		t.Fatalf("shutdown order = %v, want %v", got, wantStop)
	}
	if len(registry.Started()) != 0 {
		t.Fatalf("expected no started tasks after shutdown, got %v", registry.Started())
	}
}

func TestStartupFailureRollsBack(t *testing.T) {
	rec := &recorder{}
	registry := NewRegistry()
	if err := registry.Register("first", rec.task("first")); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := registry.Register("broken", &Task{
		Depends: []string{"first"},
		Startup: func(context.Context) error { return errors.New("boom") },
	}); err != nil {
		t.Fatalf("register broken: %v", err)
	}

	err := registry.Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Startup error = %v, want boom", err)
	}
	want := []string{"start:first", "stop:first"}
	if got := rec.snapshot(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDependencyCycleDetected(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("a", &Task{Depends: []string{"b"}}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := registry.Register("b", &Task{Depends: []string{"a"}}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	err := registry.Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Startup error = %v, want cycle", err)
	}
}

func TestPanicIsolated(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("panics", &Task{
		Startup:  func(context.Context) error { panic("kaboom") },
		Shutdown: func(context.Context) error { panic("kaboom") },
	}); err != nil {
		t.Fatalf("register panics: %v", err)
	}

	err := registry.Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Startup error = %v, want panic", err)
	}
	if err := registry.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("db", &Task{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register("db", &Task{}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if err := registry.Register("", &Task{}); err == nil {
		t.Fatal("expected empty task name to fail")
	}
	if err := registry.Register("nil", nil); err == nil {
		t.Fatal("expected nil task to fail")
	}
}

func TestCallbacksCanAccessRegistry(t *testing.T) {
	registry := NewRegistry()
	done := make(chan struct{})
	if err := registry.Register("meta", &Task{
		Startup: func(context.Context) error {
			// 旧实现持有注册表锁回调，这里会死锁。
			_ = registry.Names()
			_ = registry.Started()
			close(done)
			return nil
		},
		Shutdown: func(context.Context) error {
			_ = registry.Started()
			return nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- registry.Startup(context.Background()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("startup callback deadlocked while accessing registry")
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if err := registry.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestMissingDependencySkipped(t *testing.T) {
	rec := &recorder{}
	registry := NewRegistry()
	if err := registry.Register("app", rec.task("app", "not-registered")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "start:app" {
		t.Fatalf("order = %v", got)
	}
}
