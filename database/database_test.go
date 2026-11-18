package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

func newSQLiteGorm(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "database.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
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

func newSQLiteDatabase(t *testing.T) *gormDatabase {
	t.Helper()
	gormDB := newSQLiteGorm(t)
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	return &gormDatabase{gorm: gormDB, sqlDB: sqlDB, log: logger.NewNop()}
}

func TestConfigEnabledAndValidate(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty driver must be disabled")
	}
	if !(Config{Driver: "mysql"}).Enabled() {
		t.Fatal("mysql driver must be enabled")
	}

	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "empty", cfg: Config{}},
		{name: "mysql ok", cfg: Config{Driver: "mysql", DSN: "dsn"}},
		{name: "postgres ok", cfg: Config{Driver: "Postgres", DSN: "dsn", LogLevel: "info"}},
		{name: "mysql missing dsn", cfg: Config{Driver: "mysql", DSN: "  "}, wantErr: true},
		{name: "unsupported", cfg: Config{Driver: "sqlite", DSN: "dsn"}, wantErr: true},
		{name: "bad log level", cfg: Config{Driver: "mysql", DSN: "dsn", LogLevel: "trace"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewGormLoggerMapping(t *testing.T) {
	cases := map[string]gormlogger.LogLevel{
		"silent": gormlogger.Silent,
		"error":  gormlogger.Error,
		"info":   gormlogger.Info,
		"warn":   gormlogger.Warn,
		"":       gormlogger.Warn,
		" WARN ": gormlogger.Warn,
	}
	for name, want := range cases {
		got := newGormLogger(logger.NewNop(), name, 0).(*gormLogger)
		if got.level != want {
			t.Fatalf("level for %q = %v, want %v", name, got.level, want)
		}
		if got.slowThreshold != 500*time.Millisecond {
			t.Fatalf("default slow threshold = %v", got.slowThreshold)
		}
	}

	log := newGormLogger(logger.NewNop(), "silent", time.Second).(*gormLogger)
	if log.slowThreshold != time.Second {
		t.Fatalf("slow threshold = %v", log.slowThreshold)
	}
}

func TestGormLoggerTraceBranches(t *testing.T) {
	log, path := newDatabaseLogger(t)
	gormLog := newGormLogger(log, "info", time.Millisecond)

	// ErrRecordNotFound 不应记为查询失败，但超过阈值仍记录慢查询。
	gormLog.Trace(context.Background(), time.Now().Add(-time.Second),
		func() (string, int64) { return "SELECT 1", 0 }, gorm.ErrRecordNotFound)
	// Info 级别下的普通查询走 Debug 分支。
	gormLog.Trace(context.Background(), time.Now(),
		func() (string, int64) { return "SELECT 2", 1 }, nil)

	content := readDatabaseLog(t, log, path)
	if !strings.Contains(content, "database slow query") {
		t.Fatalf("expected slow query log: %s", content)
	}
	if !strings.Contains(content, "database query") || !strings.Contains(content, "SELECT 2") {
		t.Fatalf("expected debug query log: %s", content)
	}
	if strings.Contains(content, "database query failed") {
		t.Fatalf("record not found must not be logged as failure: %s", content)
	}
}

func TestOpenRejectsUnsupportedDriverWithoutLogger(t *testing.T) {
	if _, err := Open(Config{Driver: "sqlite", DSN: "dsn"}, nil); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestDialectorConstructors(t *testing.T) {
	if name := mysqlDialector("user:pass@tcp(127.0.0.1:3306)/db").Name(); name != "mysql" {
		t.Fatalf("mysql dialector name = %q", name)
	}
	if name := postgresDialector("host=127.0.0.1 user=postgres").Name(); name != "postgres" {
		t.Fatalf("postgres dialector name = %q", name)
	}

	if dialect, err := newDialector("MySQL", "dsn"); err != nil || dialect.Name() != "mysql" {
		t.Fatalf("newDialector mysql = %v, %v", dialect, err)
	}
	if dialect, err := newDialector("postgres", "dsn"); err != nil || dialect.Name() != "postgres" {
		t.Fatalf("newDialector postgres = %v, %v", dialect, err)
	}
	if _, err := newDialector("sqlite", "dsn"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func withDialector(t *testing.T, dialect gorm.Dialector, err error) {
	t.Helper()
	old := newDialector
	newDialector = func(string, string) (gorm.Dialector, error) { return dialect, err }
	t.Cleanup(func() { newDialector = old })
}

func TestOpenWithMockDriver(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)

	mock.ExpectPing()
	mock.ExpectPing()

	db, err := Open(Config{
		Driver:          "mysql",
		DSN:             "ignored",
		MaxOpenConns:    7,
		MaxIdleConns:    0,
		ConnMaxLifetime: time.Minute,
		LogLevel:        "silent",
	}, logger.NewNop())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if db.DB() == nil {
		t.Fatal("expected gorm db")
	}
	if db.Stats().MaxOpenConnections != 7 {
		t.Fatalf("max open conns = %d", db.Stats().MaxOpenConnections)
	}
	mock.ExpectPing()
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mock.ExpectClose()
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestOpenPingFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)

	mock.ExpectPing()
	mock.ExpectPing().WillReturnError(errors.New("ping boom"))
	mock.ExpectClose()

	if _, err := Open(Config{Driver: "mysql", DSN: "ignored"}, logger.NewNop()); err == nil {
		t.Fatal("expected ping failure")
	} else if !strings.Contains(err.Error(), "ping boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenGormFailure(t *testing.T) {
	mockDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	_ = mockDB.Close()
	withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)

	if _, err := Open(Config{Driver: "mysql", DSN: "ignored"}, logger.NewNop()); err == nil {
		t.Fatal("expected gorm open failure")
	}
}

func TestOpenDialectorFactoryFailure(t *testing.T) {
	withDialector(t, nil, errors.New("factory boom"))
	if _, err := Open(Config{Driver: "mysql", DSN: "ignored"}, logger.NewNop()); err == nil {
		t.Fatal("expected dialector factory failure")
	}
}

func TestGormDatabaseOperations(t *testing.T) {
	db := newSQLiteDatabase(t)
	ctx := context.Background()

	if db.DB() == nil {
		t.Fatal("expected gorm db")
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	stats := db.Stats()
	if stats.MaxOpenConnections < 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	if err := db.Transaction(ctx, func(tx *gorm.DB) error {
		return tx.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)").Error
	}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Transaction(ctx, func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO items (id, name) VALUES (1, 'kept')").Error
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	commitErr := errors.New("rollback")
	if err := db.Transaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO items (id, name) VALUES (2, 'dropped')").Error; err != nil {
			return err
		}
		return commitErr
	}); !errors.Is(err, commitErr) {
		t.Fatalf("rollback error = %v", err)
	}
	var count int64
	if err := db.DB().Raw("SELECT COUNT(*) FROM items WHERE id = 2").Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatal("rollback did not discard insert")
	}

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_ = db.Transaction(ctx, func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO items (id, name) VALUES (3, 'panic')").Error; err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if err := db.DB().Raw("SELECT COUNT(*) FROM items WHERE id = 3").Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatal("panic did not roll back transaction")
	}

	if err := db.Transaction(ctx, nil); err == nil {
		t.Fatal("expected nil callback error")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Ping(ctx); err == nil {
		t.Fatal("expected ping error after close")
	}
}

func TestTransactionContextHelpers(t *testing.T) {
	base := newSQLiteGorm(t)
	ctx := context.Background()

	if tx, ok := TxFrom(ctx); ok || tx != nil {
		t.Fatal("expected no transaction in context")
	}
	if conn := Conn(ctx, base); conn == nil {
		t.Fatal("expected base connection")
	}

	tx := base.Begin()
	if tx.Error != nil {
		t.Fatalf("begin: %v", tx.Error)
	}
	ctx = WithTx(ctx, tx)
	got, ok := TxFrom(ctx)
	if !ok || got == nil {
		t.Fatal("expected transaction in context")
	}
	if conn := Conn(ctx, base); conn == nil {
		t.Fatal("expected transaction connection")
	}
	_ = tx.Rollback()
}

func setDatabaseConfig(t *testing.T, values map[string]string) {
	t.Helper()
	t.Cleanup(func() { _ = config.Reload() })
	for key, value := range values {
		t.Setenv(key, value)
	}
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
}

func TestBootLifecycle(t *testing.T) {
	ctx := context.Background()

	if GetConfig() == nil {
		t.Fatal("expected configuration snapshot")
	}
	if Get() != nil {
		t.Fatal("expected nil database before startup")
	}
	if err := Startup(ctx); err != nil {
		t.Fatalf("disabled startup: %v", err)
	}
	if err := Shutdown(ctx); err != nil {
		t.Fatalf("shutdown without database: %v", err)
	}

	t.Run("invalid driver", func(t *testing.T) {
		setDatabaseConfig(t, map[string]string{
			"GOFRAME_DATABASE_DRIVER": "sqlite",
			"GOFRAME_DATABASE_DSN":    "ignored",
		})
		if err := Startup(ctx); err == nil {
			t.Fatal("expected validation error")
		}
	})

	t.Run("open error", func(t *testing.T) {
		setDatabaseConfig(t, map[string]string{
			"GOFRAME_DATABASE_DRIVER": "mysql",
			"GOFRAME_DATABASE_DSN":    "ignored",
		})
		withDialector(t, nil, errors.New("open boom"))
		if err := Startup(ctx); err == nil {
			t.Fatal("expected open error")
		}
		if Get() != nil {
			t.Fatal("expected no global database")
		}
	})

	t.Run("ping error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)
		setDatabaseConfig(t, map[string]string{
			"GOFRAME_DATABASE_DRIVER": "mysql",
			"GOFRAME_DATABASE_DSN":    "ignored",
		})
		mock.ExpectPing()
		mock.ExpectPing()
		mock.ExpectPing().WillReturnError(errors.New("startup ping boom"))
		mock.ExpectClose()

		if err := Startup(ctx); err == nil {
			t.Fatal("expected startup ping error")
		}
		if Get() != nil {
			t.Fatal("expected no global database after ping failure")
		}
	})

	t.Run("success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)
		setDatabaseConfig(t, map[string]string{
			"GOFRAME_DATABASE_DRIVER": "mysql",
			"GOFRAME_DATABASE_DSN":    "ignored",
		})
		mock.ExpectPing()
		mock.ExpectPing()
		mock.ExpectPing()

		if err := Startup(ctx); err != nil {
			t.Fatalf("Startup: %v", err)
		}
		if Get() == nil {
			t.Fatal("expected global database")
		}
		mock.ExpectClose()
		if err := Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if Get() != nil {
			t.Fatal("expected global database to be cleared")
		}
	})
}
