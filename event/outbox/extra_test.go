package outbox

import (
	"context"
	"database/sql"
	stderrors "errors"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/database"
	"github.com/god-jason/goframe/event"
)

type fakeDatabase struct {
	db *gorm.DB
}

func (f fakeDatabase) DB() *gorm.DB { return f.db }
func (f fakeDatabase) Ping(context.Context) error {
	return nil
}
func (f fakeDatabase) Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return f.db.WithContext(ctx).Transaction(fn)
}
func (f fakeDatabase) Stats() sql.DBStats { return sql.DBStats{} }
func (f fakeDatabase) Close() error       { return nil }

func setOutboxEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{
		"GOFRAME_OUTBOX_ENABLED", "GOFRAME_OUTBOX_INTERVAL", "GOFRAME_OUTBOX_BATCH_SIZE",
		"GOFRAME_OUTBOX_MAX_ATTEMPTS", "GOFRAME_OUTBOX_LEASE_TIMEOUT",
	}
	for _, key := range keys {
		_ = os.Unsetenv(key)
	}
	applied := make([]string, 0, len(values))
	for key, value := range values {
		_ = os.Setenv(key, value)
		applied = append(applied, key)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	t.Cleanup(func() {
		for _, key := range applied {
			_ = os.Unsetenv(key)
		}
		_ = config.Reload()
	})
}

func TestBootLifecycle(t *testing.T) {
	db := newTestDB(t)
	bus := &fakeBus{}
	originalDB, originalEvent := databaseGet, eventGet
	defer func() { databaseGet, eventGet = originalDB, originalEvent }()
	databaseGet = func() database.Database { return fakeDatabase{db: db} }
	eventGet = func() event.EventBus { return bus }

	setOutboxEnv(t, map[string]string{
		"GOFRAME_OUTBOX_ENABLED":       "true",
		"GOFRAME_OUTBOX_INTERVAL":      "10ms",
		"GOFRAME_OUTBOX_BATCH_SIZE":    "10",
		"GOFRAME_OUTBOX_LEASE_TIMEOUT": "1s",
	})

	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	relay := Get()
	if relay == nil {
		t.Fatal("expected global relay")
	}
	recordEvent(t, db, event.Event{Name: "boot.event"})
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
			t.Fatalf("message not published, status=%q", msg.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected nil relay after Shutdown")
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestBootDisabledAndMissingDatabase(t *testing.T) {
	setOutboxEnv(t, map[string]string{"GOFRAME_OUTBOX_ENABLED": "false"})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup disabled: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled outbox must not create relay")
	}

	originalDB := databaseGet
	defer func() { databaseGet = originalDB }()
	databaseGet = func() database.Database { return nil }
	setOutboxEnv(t, map[string]string{"GOFRAME_OUTBOX_ENABLED": "true"})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup without database: %v", err)
	}
	if Get() != nil {
		t.Fatal("relay without database must be nil")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestBootAutoMigrateFailure(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	_ = sqlDB.Close()

	originalDB := databaseGet
	defer func() { databaseGet = originalDB }()
	databaseGet = func() database.Database { return fakeDatabase{db: db} }
	setOutboxEnv(t, map[string]string{"GOFRAME_OUTBOX_ENABLED": "true"})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected auto migrate error")
	}
}

func TestRecordErrors(t *testing.T) {
	db := newTestDB(t)
	if err := Record(context.Background(), db, event.Event{Name: "bad", Payload: make(chan int)}); err == nil {
		t.Fatal("expected payload marshal error")
	}

	closed := newTestDB(t)
	sqlDB, err := closed.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	_ = sqlDB.Close()
	if err := Record(context.Background(), closed, event.Event{Name: "closed"}); err == nil {
		t.Fatal("expected create error")
	}
}

func TestAutoMigrateNilAndClosed(t *testing.T) {
	var nilRelay *Relay
	if err := nilRelay.AutoMigrate(); err == nil {
		t.Fatal("expected nil relay error")
	}

	db := newTestDB(t)
	relay := New(db, &fakeBus{}, Options{})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	_ = sqlDB.Close()
	if err := relay.AutoMigrate(); err == nil {
		t.Fatal("expected closed database error")
	}
}

func TestRelayStopEdges(t *testing.T) {
	var nilRelay *Relay
	if err := nilRelay.Stop(context.Background()); err != nil {
		t.Fatalf("nil relay Stop: %v", err)
	}

	db := newTestDB(t)
	relay := New(db, &fakeBus{}, Options{Interval: 10 * time.Millisecond})
	if err := relay.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := relay.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := relay.Start(context.Background()); err == nil {
		t.Fatal("expected already started error")
	}
	if err := relay.Stop(nil); err != nil {
		t.Fatalf("Stop(nil ctx): %v", err)
	}

	// 模拟仍有未完成的投递，Stop 超时返回错误。
	relay = New(db, &fakeBus{}, Options{Interval: time.Hour})
	if err := relay.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	relay.wg.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := relay.Stop(ctx); err == nil {
		t.Fatal("expected stop timeout")
	}
	relay.wg.Done()
}

func TestDispatchContextAndClaimErrors(t *testing.T) {
	db := newTestDB(t)
	relay := New(db, &fakeBus{}, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := relay.dispatch(ctx); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("dispatch canceled = %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	_ = sqlDB.Close()
	if err := relay.dispatch(context.Background()); err == nil {
		t.Fatal("expected claim error")
	}
}

func TestToEventErrors(t *testing.T) {
	invalidPayload := &Message{Name: "x", Payload: []byte("{bad")}
	if _, err := invalidPayload.toEvent(); err == nil {
		t.Fatal("expected payload decode error")
	}

	invalidMetadata := &Message{Name: "x", Metadata: []byte("{bad")}
	if _, err := invalidMetadata.toEvent(); err == nil {
		t.Fatal("expected metadata decode error")
	}

	withZeroTime := &Message{Name: "x"}
	evt, err := withZeroTime.toEvent()
	if err != nil {
		t.Fatalf("toEvent: %v", err)
	}
	if evt.OccurredAt.IsZero() {
		t.Fatal("expected occurred_at fallback")
	}
}

func TestPublishDecodeFailureUpdatesMessage(t *testing.T) {
	db := newTestDB(t)
	relay := New(db, &fakeBus{}, Options{MaxAttempts: 5, BaseDelay: time.Millisecond})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	msg := Message{
		ID:            "broken",
		Name:          "broken.event",
		Payload:       []byte("{bad"),
		Status:        string(StatusPublishing),
		NextAttemptAt: time.Now().UTC(),
		CreatedAt:     time.Now().UTC(),
	}
	if err := db.Create(&msg).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	relay.publish(context.Background(), &msg)
	var stored Message
	if err := db.First(&stored, "id = ?", msg.ID).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored.Attempts != 1 || stored.LastError == "" || stored.Status != string(StatusPending) {
		t.Fatalf("unexpected stored message: %+v", stored)
	}
}

func TestBackoffClampAndTruncate(t *testing.T) {
	relay := New(nil, nil, Options{BaseDelay: 5 * time.Second, MaxDelay: time.Second})
	if got := relay.backoff(1); got != time.Second {
		t.Fatalf("backoff(1) = %v, want 1s", got)
	}

	if truncateError(nil) != "" {
		t.Fatal("truncateError(nil) must be empty")
	}
	long := strings.Repeat("x", maxLastErrorLen+100)
	if got := truncateError(stderrors.New(long)); len(got) != maxLastErrorLen {
		t.Fatalf("truncated length = %d, want %d", len(got), maxLastErrorLen)
	}
}

func TestSupportsSkipLocked(t *testing.T) {
	if supportsSkipLocked(nil) {
		t.Fatal("nil database must not support skip locked")
	}
	if supportsSkipLocked(&gorm.DB{Config: &gorm.Config{}}) {
		t.Fatal("database without dialector must not support skip locked")
	}

	mysqlDB, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "user:pass@tcp(127.0.0.1:1)/db",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open mysql dialector: %v", err)
	}
	if !supportsSkipLocked(mysqlDB) {
		t.Fatal("mysql should support skip locked")
	}

	postgresDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=127.0.0.1 port=1 user=u dbname=d sslmode=disable",
	}), &gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	if !supportsSkipLocked(postgresDB) {
		t.Fatal("postgres should support skip locked")
	}
}

func TestConfigOptionsSnapshot(t *testing.T) {
	cfg := DefaultConfig()
	opts := cfg.Options()
	if opts.Interval != cfg.Interval || opts.BatchSize != cfg.BatchSize ||
		opts.MaxAttempts != cfg.MaxAttempts || opts.BaseDelay != cfg.BaseDelay ||
		opts.MaxDelay != cfg.MaxDelay || opts.LeaseTimeout != cfg.LeaseTimeout {
		t.Fatalf("options = %+v", opts)
	}
	if GetConfig() == nil {
		t.Fatal("expected current config")
	}
}
