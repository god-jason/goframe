package boot

import (
	"context"
	"errors"
	"testing"
)

// TestRegistryRunShutdownErrorCollected 覆盖 Run 中 Shutdown 失败被收集的分支。
func TestRegistryRunShutdownErrorCollected(t *testing.T) {
	rec := &recorder{}
	registry := newTestRegistry()
	boom := errors.New("shutdown boom")
	if err := registry.Register("db", &Module{
		Startup:  func(context.Context) error { rec.add("start:db"); return nil },
		Shutdown: func(context.Context) error { return boom },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := registry.Run(ctx, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// TestPackageRunDelegatesToDefault 覆盖包级 Run 委托 defaultRegistry 的函数体。
func TestPackageRunDelegatesToDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// defaultRegistry 在 boot 包测试内为空，Run 立即返回。
	if err := Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
