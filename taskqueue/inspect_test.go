package taskqueue

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInspectInterfaces(t *testing.T) {
	done := make(chan string, 8)
	Register("test.inspect", func(_ context.Context, payload []byte) error {
		done <- string(payload)
		return nil
	})
	startQueue(t, 1)

	// 投递一个长时间延迟的任务，使其停留在 scheduled 状态。
	if err := Enqueue(context.Background(), "test.inspect", []byte("later"), Delay(time.Hour)); err != nil {
		t.Fatalf("enqueue scheduled: %v", err)
	}

	if _, err := Queues(); err != nil {
		t.Fatalf("queues: %v", err)
	}
	stats, err := InspectQueues()
	if err != nil {
		t.Fatalf("inspect queues: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("expected at least one queue")
	}
	if _, err := InspectQueue("default"); err != nil {
		t.Fatalf("inspect default: %v", err)
	}
	if _, err := InspectQueue("  "); err == nil {
		t.Fatal("expected error for empty queue name")
	}
	if _, err := ListTasks("default", "bogus", 1, 10); err == nil {
		t.Fatal("expected error for unknown state")
	}

	scheduled, err := ListTasks("default", "scheduled", 0, 0)
	if err != nil {
		t.Fatalf("list scheduled: %v", err)
	}
	if len(scheduled) != 1 || scheduled[0].Type != "test.inspect" || string(scheduled[0].Payload) != "later" {
		t.Fatalf("scheduled tasks = %+v", scheduled)
	}
	id := scheduled[0].ID

	// 取消：scheduled 任务应转入归档。
	if err := CancelTask("default", id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	archived, err := ListTasks("default", "archived", 1, 10)
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != id {
		t.Fatalf("archived tasks = %+v", archived)
	}
	// 已归档任务不可再次取消。
	if err := CancelTask("default", id); err == nil {
		t.Fatal("expected error canceling archived task")
	}

	// 立即执行：archived 任务重新入队后应被消费。
	if err := RunTaskNow("default", id); err != nil {
		t.Fatalf("run now: %v", err)
	}
	select {
	case got := <-done:
		if got != "later" {
			t.Fatalf("payload = %q", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for forced run")
	}

	// 删除：新的延迟任务删除后不应再出现在 scheduled 列表。
	if err := Enqueue(context.Background(), "test.inspect", []byte("doomed"), Delay(time.Hour)); err != nil {
		t.Fatalf("enqueue doomed: %v", err)
	}
	scheduled, err = ListTasks("default", "scheduled", 1, 10)
	if err != nil {
		t.Fatalf("list scheduled again: %v", err)
	}
	if len(scheduled) != 1 {
		t.Fatalf("scheduled tasks = %+v", scheduled)
	}
	if err := DeleteTask("default", scheduled[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	scheduled, err = ListTasks("default", "scheduled", 1, 10)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(scheduled) != 0 {
		t.Fatalf("expected empty scheduled after delete, got %+v", scheduled)
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := Queues(); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("queues after shutdown = %v, want ErrNotEnabled", err)
	}
	if _, err := ListTasks("default", "pending", 1, 10); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("list after shutdown = %v, want ErrNotEnabled", err)
	}
	if err := CancelTask("default", "x"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("cancel after shutdown = %v, want ErrNotEnabled", err)
	}
	if err := DeleteTask("default", "x"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("delete after shutdown = %v, want ErrNotEnabled", err)
	}
	if err := RunTaskNow("default", "x"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("run after shutdown = %v, want ErrNotEnabled", err)
	}
}
