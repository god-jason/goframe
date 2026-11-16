package event

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/god-jason/goframe/logger"
)

func TestPublishAndSubscribe(t *testing.T) {
	bus := New(logger.NewNop())
	ctx := context.Background()

	var calls []string
	if err := bus.Subscribe("order.created", func(_ context.Context, evt Event) error {
		calls = append(calls, "first")
		if evt.Payload != 42 {
			t.Fatalf("payload = %v", evt.Payload)
		}
		if evt.OccurredAt.IsZero() {
			t.Fatal("expected OccurredAt to be filled")
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := bus.Subscribe("order.created", func(context.Context, Event) error {
		calls = append(calls, "second")
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(ctx, Event{Name: "order.created", Payload: 42}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestPublishAggregatesErrorsAndRecoversPanics(t *testing.T) {
	bus := New(logger.NewNop())
	ctx := context.Background()

	handlerErr := stderrors.New("handler failed")
	called := false
	_ = bus.Subscribe("evt", func(context.Context, Event) error { return handlerErr })
	_ = bus.Subscribe("evt", func(context.Context, Event) error { panic("boom") })
	_ = bus.Subscribe("evt", func(context.Context, Event) error {
		called = true
		return nil
	})

	err := bus.Publish(ctx, Event{Name: "evt"})
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if !stderrors.Is(err, handlerErr) {
		t.Fatalf("expected handler error in chain, got %v", err)
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic to be converted to error, got %v", err)
	}
	if !called {
		t.Fatal("expected all handlers to be executed")
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := New(logger.NewNop())
	called := false
	_ = bus.Subscribe("evt", func(context.Context, Event) error {
		called = true
		return nil
	})
	if err := bus.Unsubscribe("evt"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err := bus.Publish(context.Background(), Event{Name: "evt"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if called {
		t.Fatal("handler must not be called after unsubscribe")
	}
}

func TestSubscribeValidation(t *testing.T) {
	bus := New(logger.NewNop())
	if err := bus.Subscribe("", func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("expected empty name error")
	}
	if err := bus.Subscribe("evt", nil); err == nil {
		t.Fatal("expected nil handler error")
	}
	if err := bus.Publish(context.Background(), Event{}); err == nil {
		t.Fatal("expected empty event name error")
	}
}
