package boot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newTestRegistry() *Registry {
	return NewRegistryWithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRegistryRunLifecycle(t *testing.T) {
	rec := &recorder{}
	registry := newTestRegistry()
	if err := registry.Register("db", rec.task("db")); err != nil {
		t.Fatalf("register: %v", err)
	}

	var phase []string
	err := registry.Run(context.Background(), func(context.Context) error {
		phase = append(phase, "serve")
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	order := rec.snapshot()
	want := []string{"start:db", "stop:db"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v", order)
	}
	if len(phase) != 1 || phase[0] != "serve" {
		t.Fatalf("phase = %v", phase)
	}
	// serve 在启动之后、释放之前运行。
	if registry.Started() == nil {
		t.Fatal("started tasks missing")
	}
}

func TestRegistryRunServeError(t *testing.T) {
	rec := &recorder{}
	registry := newTestRegistry()
	if err := registry.Register("db", rec.task("db")); err != nil {
		t.Fatalf("register: %v", err)
	}

	boom := errors.New("serve boom")
	err := registry.Run(context.Background(), func(context.Context) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	order := rec.snapshot()
	if len(order) != 2 || order[0] != "start:db" || order[1] != "stop:db" {
		t.Fatalf("order = %v", order)
	}
}

func TestRegistryRunNilServeWaitsForCancel(t *testing.T) {
	rec := &recorder{}
	registry := newTestRegistry()
	if err := registry.Register("db", rec.task("db")); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { done <- registry.Run(ctx, nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	order := rec.snapshot()
	if len(order) != 2 || order[1] != "stop:db" {
		t.Fatalf("order = %v", order)
	}
}

func TestRegistryRunStartupFailureSkipsServe(t *testing.T) {
	rec := &recorder{}
	registry := newTestRegistry()
	if err := registry.Register("db", rec.task("db")); err != nil {
		t.Fatalf("register: %v", err)
	}
	boom := errors.New("startup boom")
	if err := registry.Register("cache", &Module{
		Depends:  []string{"db"},
		Startup:  func(context.Context) error { return boom },
		Shutdown: func(context.Context) error { rec.add("stop:cache"); return nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	served := false
	err := registry.Run(context.Background(), func(context.Context) error {
		served = true
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if served {
		t.Fatal("serve must not run after startup failure")
	}
	// Startup 内部回滚：db 已释放，cache 从未启动。
	order := rec.snapshot()
	if strings.Join(order, ",") != "start:db,stop:db" {
		t.Fatalf("order = %v", order)
	}
}

func TestRegistryRunWithShutdownTimeoutOption(t *testing.T) {
	registry := newTestRegistry()
	if err := registry.Register("db", (&recorder{}).task("db")); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := registry.Run(ctx, nil,
		WithShutdownTimeout(100*time.Millisecond),
		WithShutdownTimeout(0), // 非法值应被忽略
		func(*runConfig) {},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestWithSignalCancelPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx, stop := WithSignal(parent)
	defer stop()
	select {
	case <-ctx.Done():
		t.Fatal("derived ctx canceled too early")
	default:
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("derived ctx not canceled")
	}

	// nil ctx 不 panic。
	ctx2, stop2 := WithSignal(nil)
	stop2()
	if ctx2 == nil {
		t.Fatal("nil ctx should derive background")
	}
}
