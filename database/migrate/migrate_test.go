package migrate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/logger"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "migrate.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
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

func testMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "create users",
			Up: func(_ context.Context, tx *gorm.DB) error {
				return tx.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)").Error
			},
			Down: func(_ context.Context, tx *gorm.DB) error {
				return tx.Exec("DROP TABLE users").Error
			},
		},
		{
			Version: 2,
			Name:    "create audit logs",
			Up: func(_ context.Context, tx *gorm.DB) error {
				return tx.Exec("CREATE TABLE audit_logs (id INTEGER PRIMARY KEY, action TEXT)").Error
			},
			Down: func(_ context.Context, tx *gorm.DB) error {
				return tx.Exec("DROP TABLE audit_logs").Error
			},
		},
	}
}

func TestUpStatusDown(t *testing.T) {
	db := newTestDB(t)
	runner, err := New(db, WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 逆序注册，执行时必须按版本升序。
	migrations := testMigrations()
	if err := runner.Register(migrations[1], migrations[0]); err != nil {
		t.Fatalf("Register: %v", err)
	}

	applied, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 2 || applied[0].Version != 1 || applied[1].Version != 2 {
		t.Fatalf("unexpected applied order: %+v", applied)
	}
	if !db.Migrator().HasTable("users") || !db.Migrator().HasTable("audit_logs") {
		t.Fatal("expected tables to be created")
	}

	statuses, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, status := range statuses {
		if !status.Applied || status.AppliedAt.IsZero() {
			t.Fatalf("unexpected status: %+v", status)
		}
	}

	rolledBack, err := runner.Down(context.Background(), 1)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0].Version != 2 {
		t.Fatalf("unexpected rollback: %+v", rolledBack)
	}
	if db.Migrator().HasTable("audit_logs") {
		t.Fatal("expected audit_logs to be dropped")
	}
	if !db.Migrator().HasTable("users") {
		t.Fatal("expected users to remain")
	}

	if _, err := runner.Down(context.Background(), 0); err != nil {
		t.Fatalf("Down all: %v", err)
	}
	if db.Migrator().HasTable("users") {
		t.Fatal("expected users to be dropped")
	}

	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if !db.Migrator().HasTable("users") {
		t.Fatal("expected migrations to be re-applied")
	}
}

func TestRegisterValidation(t *testing.T) {
	db := newTestDB(t)
	runner, _ := New(db)

	if err := runner.Register(Migration{Version: 0, Name: "x", Up: func(context.Context, *gorm.DB) error { return nil }}); err == nil {
		t.Fatal("expected invalid version")
	}
	if err := runner.Register(Migration{Version: 1, Name: "", Up: func(context.Context, *gorm.DB) error { return nil }}); err == nil {
		t.Fatal("expected empty name error")
	}
	if err := runner.Register(Migration{Version: 1, Name: "x"}); err == nil {
		t.Fatal("expected nil up error")
	}
	if err := runner.Register(Migration{Version: 1, Name: "x", Up: func(context.Context, *gorm.DB) error { return nil }}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := runner.Register(Migration{Version: 1, Name: "x2", Up: func(context.Context, *gorm.DB) error { return nil }}); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("duplicate = %v, want ErrDuplicateVersion", err)
	}
}

func TestDownWithoutDownFunction(t *testing.T) {
	db := newTestDB(t)
	runner, _ := New(db)
	if err := runner.Register(Migration{
		Version: 1,
		Name:    "irreversible",
		Up: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE t1 (id INTEGER PRIMARY KEY)").Error
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); !errors.Is(err, ErrNoDown) {
		t.Fatalf("Down = %v, want ErrNoDown", err)
	}
}

func TestFrontendFailureStopsExecution(t *testing.T) {
	db := newTestDB(t)
	runner, _ := New(db)
	if err := runner.Register(
		Migration{Version: 1, Name: "ok", Up: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE ok_table (id INTEGER PRIMARY KEY)").Error
		}},
		Migration{Version: 2, Name: "boom", Up: func(context.Context, *gorm.DB) error {
			return errors.New("boom")
		}},
		Migration{Version: 3, Name: "later", Up: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE later_table (id INTEGER PRIMARY KEY)").Error
		}},
	); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := runner.Up(context.Background()); err == nil {
		t.Fatal("expected migration failure")
	}
	if !db.Migrator().HasTable("ok_table") {
		t.Fatal("expected first migration committed")
	}
	if db.Migrator().HasTable("later_table") {
		t.Fatal("expected later migration not executed")
	}
}

func TestLoadSQL(t *testing.T) {
	files := fstest.MapFS{
		"migrations/0001_create_users.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")},
		"migrations/0001_create_users.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE users")},
		"migrations/0002_create_audit.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE audit_logs (id INTEGER PRIMARY KEY)")},
	}
	migrations, err := LoadSQL(files, "migrations")
	if err != nil {
		t.Fatalf("LoadSQL: %v", err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
	if migrations[0].Name != "create users" {
		t.Fatalf("name = %q", migrations[0].Name)
	}
	if migrations[1].Down != nil {
		t.Fatal("expected second migration to have no down function")
	}

	db := newTestDB(t)
	runner, _ := New(db)
	if err := runner.Register(migrations...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !db.Migrator().HasTable("users") || !db.Migrator().HasTable("audit_logs") {
		t.Fatal("expected SQL migrations to create tables")
	}

	if _, err := runner.Down(context.Background(), 2); !errors.Is(err, ErrNoDown) {
		t.Fatalf("Down = %v, want ErrNoDown", err)
	}
	if !db.Migrator().HasTable("users") {
		t.Fatal("expected rollback to stop before dropping users")
	}
}

func TestLoadSQLValidation(t *testing.T) {
	if _, err := LoadSQL(nil, "."); err == nil {
		t.Fatal("expected nil filesystem error")
	}
	if _, err := LoadSQL(fstest.MapFS{}, "missing"); err == nil {
		t.Fatal("expected missing dir error")
	}
	if _, err := LoadSQL(fstest.MapFS{
		"migrations/0001_missing_down.up.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		"migrations/0001_bad.down.sql":        &fstest.MapFile{Data: []byte("SELECT 1")},
		"migrations/0001_duplicate.up.sql":    &fstest.MapFile{Data: []byte("SELECT 1")},
	}, "migrations"); err == nil {
		t.Fatal("expected duplicate up error")
	}
}
