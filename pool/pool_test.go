package pool

import (
	"context"
	stderrors "errors"
	"sync/atomic"
	"testing"
	"time"
)

func newTestPool(t *testing.T, workers, queueSize int) *Pool {
	t.Helper()
	p := New(Options{Name: "test", Workers: workers, QueueSize: queueSize})
	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = p.Stop(stopCtx)
		cancel()
	})
	return p
}

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", description)
}

func TestPoolClosesWhenParentContextCanceled(t *testing.T) {
	p := New(Options{Name: "cancel", Workers: 1, QueueSize: 2})
	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = p.Stop(stopCtx)
	}()

	cancel()
	waitFor(t, "pool marked stopped", func() bool { return !p.IsRunning() })
	if err := p.Submit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrClosed) {
		t.Fatalf("Submit after cancel = %v, want ErrClosed", err)
	}
}

func TestSubmitExecutesAllTasks(t *testing.T) {
	p := newTestPool(t, 2, 8)
	ctx := context.Background()

	var count atomic.Int64
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		if err := p.Submit(ctx, func(context.Context) error {
			count.Add(1)
			done <- struct{}{}
			return nil
		}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for task execution")
		}
	}

	waitFor(t, "all tasks completed", func() bool { return p.Stats().Completed == 10 })
	if count.Load() != 10 {
		t.Fatalf("count = %d, want 10", count.Load())
	}
}

func TestTaskPanicIsIsolated(t *testing.T) {
	var reported atomic.Int64
	p := New(Options{
		Name:      "panic-test",
		Workers:   1,
		QueueSize: 4,
		OnError:   func(error) { reported.Add(1) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = p.Stop(stopCtx)
	}()

	if err := p.Submit(ctx, func(context.Context) error { panic("boom") }); err != nil {
		t.Fatalf("Submit panic task: %v", err)
	}

	var after atomic.Bool
	if err := p.Submit(ctx, func(context.Context) error {
		after.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("Submit follow-up task: %v", err)
	}

	waitFor(t, "panic isolated", func() bool { return after.Load() })
	stats := p.Stats()
	if stats.Panics != 1 {
		t.Fatalf("panics = %d, want 1", stats.Panics)
	}
	if stats.Failed < 1 {
		t.Fatalf("failed = %d, want >= 1", stats.Failed)
	}
	if reported.Load() == 0 {
		t.Fatal("expected error handler to be called")
	}
}

func TestTrySubmitQueueFull(t *testing.T) {
	p := newTestPool(t, 1, 1)
	ctx := context.Background()

	started := make(chan struct{})
	release := make(chan struct{})
	if err := p.Submit(ctx, func(context.Context) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Submit blocking task: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking task did not start")
	}

	if err := p.TrySubmit(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("TrySubmit queued task: %v", err)
	}
	if err := p.TrySubmit(ctx, func(context.Context) error { return nil }); !stderrors.Is(err, ErrQueueFull) {
		t.Fatalf("TrySubmit full queue = %v, want ErrQueueFull", err)
	}

	close(release)
	waitFor(t, "queue drained", func() bool { return p.Stats().Queued == 0 })
}

func TestSubmitWaitsForSpaceUntilContextDone(t *testing.T) {
	p := newTestPool(t, 1, 1)
	ctx := context.Background()

	started := make(chan struct{})
	release := make(chan struct{})
	if err := p.Submit(ctx, func(context.Context) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Submit blocking task: %v", err)
	}
	<-started
	if err := p.Submit(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Submit queued task: %v", err)
	}

	submitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := p.Submit(submitCtx, func(context.Context) error { return nil }); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit full queue = %v, want deadline exceeded", err)
	}

	close(release)
}

func TestSubmitValidationAndLifecycle(t *testing.T) {
	p := New(Options{Workers: 1, QueueSize: 1})
	if err := p.Submit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrNotStarted) {
		t.Fatalf("Submit before start = %v, want ErrNotStarted", err)
	}
	if err := p.Submit(context.Background(), nil); !stderrors.Is(err, ErrNilTask) {
		t.Fatalf("Submit nil = %v, want ErrNilTask", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := p.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := p.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if err := p.Submit(context.Background(), func(context.Context) error { return nil }); !stderrors.Is(err, ErrClosed) {
		t.Fatalf("Submit after stop = %v, want ErrClosed", err)
	}
	if !stderrors.Is(p.Start(ctx), ErrClosed) {
		t.Fatal("Start after stop should return ErrClosed")
	}
	if p.IsRunning() {
		t.Fatal("stopped pool must not be running")
	}
}

func TestStopDrainsQueuedTasks(t *testing.T) {
	p := New(Options{Name: "drain", Workers: 1, QueueSize: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var count atomic.Int64
	for i := 0; i < 5; i++ {
		if err := p.Submit(ctx, func(context.Context) error {
			count.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := p.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if count.Load() != 5 {
		t.Fatalf("executed = %d, want 5", count.Load())
	}
	if stats := p.Stats(); stats.Completed != 5 {
		t.Fatalf("completed = %d, want 5", stats.Completed)
	}
}

func TestStopTimeoutCancelsPool(t *testing.T) {
	p := New(Options{Name: "timeout", Workers: 1, QueueSize: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	blocked := make(chan struct{})
	if err := p.Submit(context.Background(), func(taskCtx context.Context) error {
		close(blocked)
		<-taskCtx.Done()
		return taskCtx.Err()
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-blocked

	// 任务不响应 Stop 的 ctx，但会被池上下文的取消信号打断。
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	if err := p.Stop(stopCtx); !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v, want deadline exceeded", err)
	}

	waitFor(t, "worker exits after cancellation", func() bool { return !p.IsRunning() })
}
