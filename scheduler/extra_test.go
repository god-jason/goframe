package scheduler

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/god-jason/goframe/config"
)

func TestNewLocationAndDefaults(t *testing.T) {
	if _, err := New(nil, Config{Location: "Not/AZone"}); err == nil {
		t.Fatal("expected invalid location error")
	}

	cron, err := New(nil, Config{Location: "UTC", JobTimeout: time.Second})
	if err != nil {
		t.Fatalf("New with UTC: %v", err)
	}
	if cron.log == nil || cron.timeout != time.Second {
		t.Fatalf("unexpected cron: %+v", cron)
	}
}

func TestRunSkipsWhenPreviousActive(t *testing.T) {
	cron := newTestCron(t, time.Second)
	e := &entry{spec: "@every 1s"}
	e.running.Store(true)
	called := false
	cron.run("job", e, func(context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Fatal("job must be skipped while previous run is active")
	}
}

func TestRunWithoutStartedContext(t *testing.T) {
	cron := newTestCron(t, time.Second)
	e := &entry{spec: "@every 1s"}
	var sawDeadline atomic.Bool
	cron.run("job", e, func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); ok {
			sawDeadline.Store(true)
		}
		return nil
	})
	if !sawDeadline.Load() {
		t.Fatal("expected job context with deadline")
	}
	if e.running.Load() {
		t.Fatal("running flag must be reset after run")
	}
}

func TestRunJobError(t *testing.T) {
	cron := newTestCron(t, time.Second)
	e := &entry{spec: "@every 1s"}
	cron.run("job", e, func(context.Context) error { return context.Canceled })
	if e.running.Load() {
		t.Fatal("running flag must be reset after error")
	}
}

func TestStopTimeoutReturnsContextError(t *testing.T) {
	cron := newTestCron(t, time.Second)
	release := make(chan struct{})
	running := make(chan struct{}, 1)
	if err := cron.Register("blocking", "@every 10ms", func(context.Context) error {
		select {
		case running <- struct{}{}:
		default:
		}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := cron.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-running:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := cron.Stop(stopCtx); err == nil {
		t.Fatal("expected stop timeout error")
	}
	close(release)
}

func TestJobsIncludesNextWhenStarted(t *testing.T) {
	cron := newTestCron(t, time.Second)
	if err := cron.Register("job", "@every 1h", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := cron.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	jobs := cron.Jobs()
	if len(jobs) != 1 || jobs[0].Next.IsZero() {
		t.Fatalf("jobs = %+v", jobs)
	}
	stopCron(t, cron)
}

func TestSchedulerBootLifecycle(t *testing.T) {
	_ = os.Setenv("GOFRAME_SCHEDULER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_SCHEDULER_JOB_TIMEOUT", "1s")
	_ = os.Setenv("GOFRAME_SCHEDULER_LOCATION", "UTC")
	defer func() {
		_ = os.Unsetenv("GOFRAME_SCHEDULER_ENABLED")
		_ = os.Unsetenv("GOFRAME_SCHEDULER_JOB_TIMEOUT")
		_ = os.Unsetenv("GOFRAME_SCHEDULER_LOCATION")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if GetConfig().JobTimeout != time.Second {
		t.Fatalf("unexpected config: %+v", GetConfig())
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("expected global scheduler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil scheduler after Shutdown")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestSchedulerBootDisabled(t *testing.T) {
	_ = os.Setenv("GOFRAME_SCHEDULER_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("GOFRAME_SCHEDULER_ENABLED")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled scheduler must not be created")
	}
}

func TestSchedulerBootInvalidTimeout(t *testing.T) {
	_ = os.Setenv("GOFRAME_SCHEDULER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_SCHEDULER_JOB_TIMEOUT", "0s")
	defer func() {
		_ = os.Unsetenv("GOFRAME_SCHEDULER_ENABLED")
		_ = os.Unsetenv("GOFRAME_SCHEDULER_JOB_TIMEOUT")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected job_timeout validation error")
	}
}

func TestSchedulerBootInvalidLocation(t *testing.T) {
	_ = os.Setenv("GOFRAME_SCHEDULER_ENABLED", "true")
	_ = os.Setenv("GOFRAME_SCHEDULER_JOB_TIMEOUT", "1s")
	_ = os.Setenv("GOFRAME_SCHEDULER_LOCATION", "Bad/Zone")
	defer func() {
		_ = os.Unsetenv("GOFRAME_SCHEDULER_ENABLED")
		_ = os.Unsetenv("GOFRAME_SCHEDULER_JOB_TIMEOUT")
		_ = os.Unsetenv("GOFRAME_SCHEDULER_LOCATION")
		_ = config.Reload()
	}()
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected invalid location error from Startup")
	}
}

func TestShutdownWithoutInstance(t *testing.T) {
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
