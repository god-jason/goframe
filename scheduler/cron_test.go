package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/god-jason/goframe/logger"
)

func newTestCron(t *testing.T, timeout time.Duration) *Cron {
	t.Helper()
	cron, err := New(logger.NewNop(), Config{Enabled: true, JobTimeout: timeout})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cron
}

func stopCron(t *testing.T, cron *Cron) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cron.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	cron := newTestCron(t, time.Second)

	if err := cron.Register("", "@every 1s", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected empty name error")
	}
	if err := cron.Register("job", "", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected empty spec error")
	}
	if err := cron.Register("job", "@every 1s", nil); err == nil {
		t.Fatal("expected nil handler error")
	}
	if err := cron.Register("job", "not-a-cron", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected invalid spec error")
	}
	if err := cron.Register("job", "@every 1s", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := cron.Register("job", "@every 1s", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected duplicate job error")
	}

	if err := cron.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cron.Register("late", "@every 1s", func(context.Context) error { return nil }); err == nil {
		t.Fatal("expected register after start error")
	}
	stopCron(t, cron)
}

func TestCronRunsJob(t *testing.T) {
	cron := newTestCron(t, time.Second)

	var count int32
	ran := make(chan struct{}, 1)
	if err := cron.Register("tick", "@every 50ms", func(context.Context) error {
		atomic.AddInt32(&count, 1)
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := cron.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("expected job to run")
	}
	stopCron(t, cron)

	if atomic.LoadInt32(&count) == 0 {
		t.Fatal("expected job invocation count greater than zero")
	}
}

func TestCronJobTimeout(t *testing.T) {
	cron := newTestCron(t, 20*time.Millisecond)

	deadline := make(chan struct{}, 1)
	if err := cron.Register("slow", "@every 100ms", func(ctx context.Context) error {
		<-ctx.Done()
		select {
		case deadline <- struct{}{}:
		default:
		}
		return ctx.Err()
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := cron.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-deadline:
	case <-time.After(3 * time.Second):
		t.Fatal("expected job context to hit its deadline")
	}
	stopCron(t, cron)
}

func TestCronRecoversJobPanic(t *testing.T) {
	cron := newTestCron(t, time.Second)

	var count int32
	if err := cron.Register("panic", "@every 30ms", func(context.Context) error {
		atomic.AddInt32(&count, 1)
		panic("boom")
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := cron.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&count) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&count) == 0 {
		t.Fatal("expected panicking job to be invoked")
	}
	stopCron(t, cron)
}

func TestJobsSnapshot(t *testing.T) {
	cron := newTestCron(t, time.Second)
	if err := cron.Register("job", "@every 1m", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}

	jobs := cron.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs[0].Name != "job" || jobs[0].Spec != "@every 1m" || jobs[0].Running {
		t.Fatalf("unexpected job: %+v", jobs[0])
	}
}

func TestStopBeforeStart(t *testing.T) {
	cron := newTestCron(t, time.Second)
	if err := cron.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := cron.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cron.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	stopCron(t, cron)
}
