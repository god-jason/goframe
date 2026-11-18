package outbox

import (
	"context"
	stderrors "errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/event"
)

// fakeBus 记录已投递事件，并可按配置模拟投递失败。
type fakeBus struct {
	mu         sync.Mutex
	events     []event.Event
	failFirst  int
	alwaysFail bool
}

func (b *fakeBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	if b.alwaysFail {
		return stderrors.New("publish failed")
	}
	if b.failFirst > 0 {
		b.failFirst--
		return stderrors.New("publish failed")
	}
	return nil
}

func (b *fakeBus) Subscribe(string, event.EventHandler) error { return nil }
func (b *fakeBus) Unsubscribe(string) error                   { return nil }

func (b *fakeBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "outbox.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:  gormlogger.Default.LogMode(gormlogger.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func recordEvent(t *testing.T, db *gorm.DB, evt event.Event) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		return Record(context.Background(), tx, evt)
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestRecordAndDeliverSuccess(t *testing.T) {
	db := newTestDB(t)
	bus := &fakeBus{}
	relay := New(db, bus, Options{})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	recordEvent(t, db, event.Event{
		Name:     "order.created",
		Payload:  map[string]any{"id": "order-1"},
		Metadata: map[string]string{"trace": "t-1"},
	})

	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if bus.count() != 1 {
		t.Fatalf("published = %d, want 1", bus.count())
	}
	evt := bus.events[0]
	if evt.Name != "order.created" {
		t.Fatalf("event name = %q", evt.Name)
	}
	payload, ok := evt.Payload.(map[string]any)
	if !ok || payload["id"] != "order-1" {
		t.Fatalf("payload = %#v", evt.Payload)
	}
	if evt.Metadata["trace"] != "t-1" {
		t.Fatalf("metadata = %#v", evt.Metadata)
	}

	var msg Message
	if err := db.First(&msg).Error; err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msg.Status != string(StatusPublished) {
		t.Fatalf("status = %q, want published", msg.Status)
	}
	if msg.PublishedAt == nil || msg.PublishedAt.IsZero() {
		t.Fatal("expected published_at to be set")
	}
	if msg.Attempts != 0 || msg.LastError != "" {
		t.Fatalf("attempts=%d last_error=%q", msg.Attempts, msg.LastError)
	}
}

func TestRetryWithBackoffThenSuccess(t *testing.T) {
	db := newTestDB(t)
	bus := &fakeBus{failFirst: 1}
	relay := New(db, bus, Options{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    time.Second,
	})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	recordEvent(t, db, event.Event{Name: "order.created", Payload: "payload"})

	started := time.Now().UTC()
	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	var msg Message
	if err := db.First(&msg).Error; err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msg.Status != string(StatusPending) {
		t.Fatalf("status = %q, want pending", msg.Status)
	}
	if msg.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", msg.Attempts)
	}
	if msg.LastError == "" {
		t.Fatal("expected last_error to be recorded")
	}
	if delay := msg.NextAttemptAt.Sub(started); delay < 90*time.Millisecond {
		t.Fatalf("next attempt delay = %v, want >= 100ms", delay)
	}

	// 未到重试时间，不应再次投递。
	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if bus.count() != 1 {
		t.Fatalf("published = %d before retry deadline, want 1", bus.count())
	}

	// 模拟退避时间已过。
	if err := db.Model(&Message{}).Where("id = ?", msg.ID).
		Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatalf("rewind next_attempt_at: %v", err)
	}
	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("retry dispatch: %v", err)
	}
	if bus.count() != 2 {
		t.Fatalf("published = %d after retry, want 2", bus.count())
	}
	if err := db.First(&msg, "id = ?", msg.ID).Error; err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msg.Status != string(StatusPublished) {
		t.Fatalf("status = %q, want published", msg.Status)
	}
	if msg.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", msg.Attempts)
	}
}

func TestExceedMaxAttempts(t *testing.T) {
	db := newTestDB(t)
	bus := &fakeBus{alwaysFail: true}
	relay := New(db, bus, Options{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Second,
	})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	recordEvent(t, db, event.Event{Name: "order.created"})

	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	var msg Message
	if err := db.First(&msg).Error; err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msg.Status != string(StatusPending) || msg.Attempts != 1 {
		t.Fatalf("after first failure: status=%q attempts=%d", msg.Status, msg.Attempts)
	}

	if err := db.Model(&Message{}).Where("id = ?", msg.ID).
		Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatalf("rewind next_attempt_at: %v", err)
	}
	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if err := db.First(&msg, "id = ?", msg.ID).Error; err != nil {
		t.Fatalf("query message: %v", err)
	}
	if msg.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want failed", msg.Status)
	}
	if msg.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", msg.Attempts)
	}

	// 已达最大次数，后续不再投递。
	if err := relay.dispatch(context.Background()); err != nil {
		t.Fatalf("third dispatch: %v", err)
	}
	if bus.count() != 2 {
		t.Fatalf("published = %d, want 2", bus.count())
	}
}

func TestBackoffCapped(t *testing.T) {
	relay := New(nil, nil, Options{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second})
	cases := map[int]time.Duration{
		1:  100 * time.Millisecond,
		2:  200 * time.Millisecond,
		3:  400 * time.Millisecond,
		4:  800 * time.Millisecond,
		5:  time.Second,
		20: time.Second,
	}
	for attempts, want := range cases {
		if got := relay.backoff(attempts); got != want {
			t.Fatalf("backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestStartStop(t *testing.T) {
	db := newTestDB(t)
	bus := &fakeBus{}
	relay := New(db, bus, Options{Interval: 10 * time.Millisecond})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	recordEvent(t, db, event.Event{Name: "order.created"})

	ctx := context.Background()
	if err := relay.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var msg Message
		if err := db.First(&msg).Error; err != nil {
			t.Fatalf("query message: %v", err)
		}
		if msg.Status == string(StatusPublished) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message not published in time, status=%q", msg.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := relay.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := relay.Start(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := relay.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestRelayValidation(t *testing.T) {
	relay := New(nil, nil, Options{})
	if err := relay.AutoMigrate(); err == nil {
		t.Fatal("expected AutoMigrate error for nil database")
	}
	if err := relay.Start(context.Background()); err == nil {
		t.Fatal("expected Start error for nil database")
	}

	db := newTestDB(t)
	relay = New(db, nil, Options{})
	if err := relay.Start(context.Background()); err == nil {
		t.Fatal("expected Start error for nil event bus")
	}
}

func TestRecordValidation(t *testing.T) {
	if err := Record(context.Background(), nil, event.Event{Name: "x"}); err == nil {
		t.Fatal("expected error for nil transaction")
	}
	db := newTestDB(t)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return Record(context.Background(), tx, event.Event{})
	}); err == nil {
		t.Fatal("expected error for empty event name")
	}
}
