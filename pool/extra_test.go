package pool

import (
	"context"
	stderrors "errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
)

func TestNewDefaultsAndAccessors(t *testing.T) {
	p := New(Options{})
	if p.Name() != "pool" {
		t.Fatalf("name = %q, want pool", p.Name())
	}
	if p.Workers() != runtime.NumCPU()*2 {
		t.Fatalf("workers = %d, want %d", p.Workers(), runtime.NumCPU()*2)
	}
	if stats := p.Stats(); stats.QueueSize != defaultQueueSize {
		t.Fatalf("queue size = %d, want %d", stats.QueueSize, defaultQueueSize)
	}
	if p.IsRunning() {
		t.Fatal("pool must not be running before Start")
	}

	capped := New(Options{Workers: maxWorkers + 100, QueueSize: 2})
	if capped.Workers() != maxWorkers {
		t.Fatalf("workers = %d, want %d", capped.Workers(), maxWorkers)
	}
}

func TestStopBeforeStart(t *testing.T) {
	p := New(Options{Workers: 1})
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before start: %v", err)
	}
	if err := p.Start(context.Background()); !stderrors.Is(err, ErrClosed) {
		t.Fatalf("Start after stop = %v, want ErrClosed", err)
	}
	if err := p.TrySubmit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrClosed) {
		t.Fatalf("TrySubmit after stop = %v, want ErrClosed", err)
	}
}

func TestTrySubmitValidation(t *testing.T) {
	p := New(Options{Workers: 1, QueueSize: 1})
	if err := p.TrySubmit(context.Background(), nil); !stderrors.Is(err, ErrNilTask) {
		t.Fatalf("TrySubmit nil = %v, want ErrNilTask", err)
	}
	if err := p.TrySubmit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrNotStarted) {
		t.Fatalf("TrySubmit before start = %v, want ErrNotStarted", err)
	}

	if err := p.Start(nil); err != nil {
		t.Fatalf("Start(nil): %v", err)
	}
	if err := p.TrySubmit(nil, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("TrySubmit nil ctx: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestTaskErrorReportedWithoutHandler(t *testing.T) {
	p := New(Options{Workers: 1, QueueSize: 2})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	done := make(chan struct{})
	if err := p.Submit(context.Background(), func(context.Context) error {
		defer close(done)
		return stderrors.New("task failed")
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not run")
	}
	waitFor(t, "failed task counted", func() bool { return p.Stats().Failed == 1 })
}

func TestErrorHandlerPanicIsIsolated(t *testing.T) {
	var calls int
	p := New(Options{
		Workers:   1,
		QueueSize: 2,
		OnError:   func(error) { calls++; panic("handler boom") },
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	done := make(chan struct{})
	if err := p.Submit(context.Background(), func(context.Context) error {
		defer close(done)
		return stderrors.New("task failed")
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not run")
	}
	waitFor(t, "error handler called", func() bool {
		p.Stats()
		return calls == 1
	})

	// panic 后 worker 仍可继续处理任务。
	after := make(chan struct{})
	if err := p.Submit(context.Background(), func(context.Context) error {
		close(after)
		return nil
	}); err != nil {
		t.Fatalf("Submit follow-up: %v", err)
	}
	select {
	case <-after:
	case <-time.After(2 * time.Second):
		t.Fatal("worker stopped after error handler panic")
	}
}

func TestSubmitAfterClose(t *testing.T) {
	p := New(Options{Workers: 1, QueueSize: 1})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := p.Submit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrClosed) {
		t.Fatalf("Submit after close = %v, want ErrClosed", err)
	}
}

func TestBootLifecycle(t *testing.T) {
	_ = os.Setenv("GOFRAME_POOL_WORKERS", "1")
	_ = os.Setenv("GOFRAME_POOL_QUEUE_SIZE", "4")
	defer func() {
		_ = os.Unsetenv("GOFRAME_POOL_WORKERS")
		_ = os.Unsetenv("GOFRAME_POOL_QUEUE_SIZE")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if GetConfig().Workers != 1 || GetConfig().QueueSize != 4 {
		t.Fatalf("unexpected config: %+v", GetConfig())
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	instance := Get()
	if instance == nil || !instance.IsRunning() {
		t.Fatal("expected running global pool")
	}
	if instance.Workers() != 1 {
		t.Fatalf("workers = %d, want 1", instance.Workers())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil global pool after Shutdown")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}
