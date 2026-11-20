package outbox

// 本文件补充 event/outbox 包的边界分支覆盖。
//
// 逻辑不可达、保留的防御分支：
//   - outbox.go:33 Record 中 metadata 编码失败分支：ev.Metadata 类型为
//     map[string]string，encoding/json 对该类型编码永不失败，无法构造触发。

import (
	"context"
	stderrors "errors"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/database"
	"github.com/god-jason/goframe/event"
)

func TestStartupRelayStartFailure(t *testing.T) {
	db := newTestDB(t)
	originalDB, originalEvent := databaseGet, eventGet
	defer func() { databaseGet, eventGet = originalDB, originalEvent }()
	databaseGet = func() database.Database { return fakeDatabase{db: db} }
	eventGet = func() event.EventBus { return nil }

	setOutboxEnv(t, map[string]string{"GOFRAME_OUTBOX_ENABLED": "true"})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected start error when event bus is missing")
	}
	if Get() != nil {
		t.Fatal("relay must stay nil after failed startup")
	}
}

func TestStartWithNilContext(t *testing.T) {
	db := newTestDB(t)
	relay := New(db, &fakeBus{}, Options{Interval: time.Hour})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := relay.Start(nil); err != nil {
		t.Fatalf("Start(nil): %v", err)
	}
	if err := relay.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// cancelOnPublishBus 在首次投递成功后取消上下文，
// 触发 dispatch 在消息之间检测到 ctx 已取消的分支。
type cancelOnPublishBus struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	published int
}

func (b *cancelOnPublishBus) Publish(_ context.Context, _ event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published++
	if b.published == 1 {
		b.cancel()
	}
	return nil
}

func (b *cancelOnPublishBus) Subscribe(string, event.EventHandler) error { return nil }
func (b *cancelOnPublishBus) Unsubscribe(string) error                   { return nil }

func TestDispatchContextCanceledBetweenMessages(t *testing.T) {
	db := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := &cancelOnPublishBus{cancel: cancel}
	relay := New(db, bus, Options{})
	if err := relay.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	recordEvent(t, db, event.Event{Name: "first.event"})
	recordEvent(t, db, event.Event{Name: "second.event"})

	if err := relay.dispatch(ctx); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("dispatch = %v, want context canceled", err)
	}
	if bus.published != 1 {
		t.Fatalf("published = %d, want 1", bus.published)
	}
}

func newMockedMySQLDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sqlmock: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open mysql dialector: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, mock
}

func TestClaimWithSkipLockedAndFindError(t *testing.T) {
	// mysql 方言下领取查询应追加 FOR UPDATE SKIP LOCKED 子句。
	db, mock := newMockedMySQLDB(t)
	relay := New(db, &fakeBus{}, Options{})
	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE SKIP LOCKED").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow("m-1", "mock.event"))
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	messages, err := relay.claim(context.Background())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "m-1" {
		t.Fatalf("messages = %+v", messages)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}

	// 领取事务内查询失败时错误应沿事务向上传播。
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT").WillReturnError(stderrors.New("query failed"))
	mock.ExpectRollback()
	if _, err := relay.claim(context.Background()); err == nil {
		t.Fatal("expected claim error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
