package event

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewWithNilLogger(t *testing.T) {
	bus := New(nil)
	if err := bus.Publish(context.Background(), Event{Name: "unknown"}); err != nil {
		t.Fatalf("Publish to unknown event: %v", err)
	}
	if err := bus.Unsubscribe("unknown"); err != nil {
		t.Fatalf("Unsubscribe unknown: %v", err)
	}
}

func TestGlobalLifecycle(t *testing.T) {
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	bus := Get()
	if bus == nil {
		t.Fatal("expected global bus after Startup")
	}
	if err := bus.Subscribe("evt", func(context.Context, Event) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("second Startup: %v", err)
	}
	if Get() == bus {
		t.Fatal("expected Startup to replace global bus")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil bus after Shutdown")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestPublishWithoutSubscribers(t *testing.T) {
	bus := New(nil)
	evt := Event{Name: "nobody", Payload: 1}
	if err := bus.Publish(context.Background(), evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestResubscribeAfterUnsubscribe(t *testing.T) {
	bus := New(nil)
	var calls atomic.Int32
	handler := func(context.Context, Event) error {
		calls.Add(1)
		return nil
	}
	for i := 0; i < 3; i++ {
		if err := bus.Subscribe("evt", handler); err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
	}
	if err := bus.Publish(context.Background(), Event{Name: "evt"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	if err := bus.Unsubscribe("evt"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err := bus.Subscribe("evt", handler); err != nil {
		t.Fatalf("Subscribe after unsubscribe: %v", err)
	}
	if err := bus.Publish(context.Background(), Event{Name: "evt"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestConcurrentPublishAndSubscribe(t *testing.T) {
	bus := New(nil)
	var total atomic.Int64
	const workers = 8

	for i := 0; i < workers; i++ {
		if err := bus.Subscribe("concurrent", func(context.Context, Event) error {
			total.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := bus.Publish(context.Background(), Event{Name: "concurrent"}); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := total.Load(); got != workers*workers*20 {
		t.Fatalf("handler calls = %d, want %d", got, workers*workers*20)
	}
}
