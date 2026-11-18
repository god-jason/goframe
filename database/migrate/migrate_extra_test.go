package migrate

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"gorm.io/gorm"

	"github.com/god-jason/goframe/logger"
)

func failQueries(db *gorm.DB) {
	db.Callback().Query().Before("gorm:query").Register("test:query_failure", func(tx *gorm.DB) {
		tx.AddError(errors.New("forced query failure"))
	})
}

func createTableMigration(version int64, name, table string) Migration {
	return Migration{
		Version: version,
		Name:    name,
		Up: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)").Error
		},
		Down: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("DROP TABLE " + table).Error
		},
	}
}

func TestNewValidationAndWithTable(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected nil database error")
	}

	db := newTestDB(t)
	runner, err := New(db, WithTable(""), WithTable("custom_migrations"), WithLogger(logger.NewNop()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if runner.table != "custom_migrations" {
		t.Fatalf("table = %q", runner.table)
	}
	if err := runner.Register(createTableMigration(1, "create custom", "custom_items")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !db.Migrator().HasTable("custom_migrations") {
		t.Fatal("expected custom version table")
	}
	if db.Migrator().HasTable(defaultTable) {
		t.Fatal("default version table must not be created")
	}

	ignored, _ := New(db, WithTable(""))
	if ignored.table != defaultTable {
		t.Fatalf("empty table option must be ignored, got %q", ignored.table)
	}
}

func TestUpSkipsAppliedMigrations(t *testing.T) {
	runner, err := New(newTestDB(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.Register(createTableMigration(1, "first", "skip_first")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	applied, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("expected no migrations on second Up, got %d", len(applied))
	}
}

func TestRunnerErrorsOnClosedDatabase(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close()

	runner, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.Register(createTableMigration(1, "closed", "closed_table")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err == nil {
		t.Fatal("expected Up error")
	}
	if _, err := runner.Down(context.Background(), 0); err == nil {
		t.Fatal("expected Down error")
	}
	if _, err := runner.Status(context.Background()); err == nil {
		t.Fatal("expected Status error")
	}
}

func TestRunnerErrorsWhenVersionTableQueryFails(t *testing.T) {
	db := newTestDB(t)
	runner, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.Register(createTableMigration(1, "query", "query_table")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	failQueries(db)

	if _, err := runner.Up(context.Background()); err == nil {
		t.Fatal("expected Up records error")
	}
	if _, err := runner.Status(context.Background()); err == nil {
		t.Fatal("expected Status records error")
	}
	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("expected Down records error")
	}
}

func TestDownUnregisteredMigration(t *testing.T) {
	db := newTestDB(t)
	runner, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := db.Table(defaultTable).Create(&Record{
		Version:   99,
		Name:      "orphan",
		AppliedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("insert record: %v", err)
	}

	if _, err := runner.Down(context.Background(), 1); err == nil {
		t.Fatal("expected unregistered migration error")
	}
}

func TestDownFunctionFailureRollsBack(t *testing.T) {
	db := newTestDB(t)
	runner, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	downErr := errors.New("down boom")
	if err := runner.Register(Migration{
		Version: 1,
		Name:    "failure",
		Up: func(_ context.Context, tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE down_failure (id INTEGER PRIMARY KEY)").Error
		},
		Down: func(context.Context, *gorm.DB) error {
			return downErr
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); !errors.Is(err, downErr) {
		t.Fatalf("Down = %v, want wrapped down error", err)
	}
	if !db.Migrator().HasTable("down_failure") {
		t.Fatal("expected failed rollback to keep table")
	}
	statuses, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Applied {
		t.Fatalf("expected record to remain after rollback failure: %+v", statuses)
	}
}

func TestLoadSQLDefaultDirAndIgnoredEntries(t *testing.T) {
	files := fstest.MapFS{
		"0001_root.up.sql":              &fstest.MapFile{Data: []byte("CREATE TABLE root_items (id INTEGER PRIMARY KEY)")},
		"README.md":                     &fstest.MapFile{Data: []byte("docs")},
		"nested/0009_nested.up.sql":     &fstest.MapFile{Data: []byte("SELECT 1")},
		"migrations/0002_skip.down.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	files["migrations/0002_skip.up.sql"] = &fstest.MapFile{Data: []byte("SELECT 1")}
	files["migrations/sub/0003_deep.up.sql"] = &fstest.MapFile{Data: []byte("SELECT 1")}

	root, err := LoadSQL(files, "")
	if err != nil {
		t.Fatalf("LoadSQL root: %v", err)
	}
	if len(root) != 1 || root[0].Version != 1 {
		t.Fatalf("unexpected root migrations: %+v", root)
	}

	nested, err := LoadSQL(files, "nested")
	if err != nil {
		t.Fatalf("LoadSQL nested: %v", err)
	}
	if len(nested) != 1 || nested[0].Version != 9 {
		t.Fatalf("unexpected nested migrations: %+v", nested)
	}

	migrations, err := LoadSQL(files, "migrations")
	if err != nil {
		t.Fatalf("LoadSQL migrations: %v", err)
	}
	if len(migrations) != 1 || migrations[0].Version != 2 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
}

func TestLoadSQLInvalidVersions(t *testing.T) {
	if _, err := LoadSQL(fstest.MapFS{
		"migrations/0_zero.up.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}, "migrations"); err == nil {
		t.Fatal("expected invalid version error")
	}
	if _, err := LoadSQL(fstest.MapFS{
		"migrations/99999999999999999999_overflow.up.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}, "migrations"); err == nil {
		t.Fatal("expected overflow version error")
	}
}

func TestLoadSQLMissingUpAndDuplicateDown(t *testing.T) {
	if _, err := LoadSQL(fstest.MapFS{
		"migrations/0001_only.down.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}, "migrations"); err == nil {
		t.Fatal("expected missing up error")
	}
	if _, err := LoadSQL(fstest.MapFS{
		"migrations/0001_dup.up.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		"migrations/0001_a.down.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
		"migrations/0001_b.down.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
	}, "migrations"); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("duplicate down = %v, want ErrDuplicateVersion", err)
	}
}

type readErrorFS struct {
	entry fs.DirEntry
}

func (readErrorFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

func (f readErrorFS) ReadDir(string) ([]fs.DirEntry, error) {
	return []fs.DirEntry{f.entry}, nil
}

func (readErrorFS) ReadFile(string) ([]byte, error) {
	return nil, errors.New("read failed")
}

type fakeDirEntry string

func (e fakeDirEntry) Name() string             { return string(e) }
func (fakeDirEntry) IsDir() bool                { return false }
func (fakeDirEntry) Type() fs.FileMode          { return 0 }
func (fakeDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("no info") }

func TestLoadSQLReadFailure(t *testing.T) {
	if _, err := LoadSQL(readErrorFS{entry: fakeDirEntry("0001_read.up.sql")}, "migrations"); err == nil {
		t.Fatal("expected read error")
	}
}

func TestLoadSQLDownExecution(t *testing.T) {
	files := fstest.MapFS{
		"migrations/0001_create.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE sql_down (id INTEGER PRIMARY KEY)")},
		"migrations/0001_create.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE sql_down")},
	}
	migrations, err := LoadSQL(files, "migrations")
	if err != nil {
		t.Fatalf("LoadSQL: %v", err)
	}
	db := newTestDB(t)
	runner, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.Register(migrations...); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := runner.Down(context.Background(), 1); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if db.Migrator().HasTable("sql_down") {
		t.Fatal("expected SQL down migration to drop table")
	}
}
