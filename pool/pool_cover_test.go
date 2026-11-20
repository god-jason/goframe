package pool

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestSubmitNilContext 覆盖 Submit/Stop 在 ctx 为 nil 时回退 context.Background() 的分支。
func TestSubmitNilContext(t *testing.T) {
	p := New(Options{Name: "nil-ctx", Workers: 1, QueueSize: 8})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	finished := make(chan struct{})
	if err := p.Submit(nil, func(context.Context) error { close(finished); return nil }); err != nil {
		t.Fatalf("Submit(nil ctx): %v", err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("任务未在超时内执行")
	}
	if err := p.Stop(nil); err != nil {
		t.Fatalf("Stop(nil ctx): %v", err)
	}
}

// TestSubmitBlockedThenStop 覆盖队列满时 Submit 在 select 中命中 <-p.done 返回 ErrClosed 的分支。
func TestSubmitBlockedThenStop(t *testing.T) {
	p := New(Options{Name: "closed-full", Workers: 1, QueueSize: 1})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx := context.Background()

	workerBusy := make(chan struct{})
	release := make(chan struct{})
	if err := p.Submit(ctx, func(context.Context) error { close(workerBusy); <-release; return nil }); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	<-workerBusy // worker 已取出任务并阻塞，队列腾出空位

	// 填满队列，使后续 Submit 阻塞在 select。
	if err := p.Submit(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	for len(p.jobs) == 0 {
		runtime.Gosched()
	}

	blocked := make(chan error, 1)
	go func() { blocked <- p.Submit(ctx, func(context.Context) error { return nil }) }()
	time.Sleep(50 * time.Millisecond) // 确保第三个 Submit 已进入 select 等待

	stopErr := make(chan error, 1)
	go func() { stopErr <- p.Stop(context.Background()) }()

	select {
	case err := <-blocked:
		if err != ErrClosed {
			t.Fatalf("阻塞 Submit 返回 %v, want ErrClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("阻塞 Submit 未在关闭后返回")
	}
	close(release) // 放行阻塞任务，让 worker 排空后退出
	select {
	case err := <-stopErr:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop 未在超时内返回")
	}
}

// 说明：boot.go 中 Startup 对 instance.Start 返回错误的分支不可达——
// Start 仅在池已停止时返回 ErrClosed，而 Startup 刚创建的池必然处于未停止状态。
