package database

// 本文件补充 database 包缺失分支：
//   - database.go:66 NowFunc 闭包（通过 gorm.DB.NowFunc 触发）
//   - database.go:74 gormDB.DB() 失败（ConnPool 非 *sql.DB 的假 dialector）
//   - database.go:141 sql.DB.Close 失败（sqlmock 连接关闭报错）

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/god-jason/goframe/logger"
)

// TestOpenNowFuncUTC 验证 Open 注入的 NowFunc 返回 UTC 时间。
func TestOpenNowFuncUTC(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	withDialector(t, mysql.New(mysql.Config{Conn: mockDB, SkipInitializeWithVersion: true}), nil)

	// gorm.Open 自动 Ping 与 Open 内的显式 Ping 各消耗一次期望。
	mock.ExpectPing()
	mock.ExpectPing()

	db, err := Open(Config{Driver: "mysql", DSN: "ignored"}, logger.NewNop())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := db.DB().NowFunc(); got.Location() != time.UTC {
		t.Fatalf("NowFunc location = %v, want UTC", got.Location())
	}
	mock.ExpectClose()
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// fakeConnPool 实现 gorm.ConnPool 但不是 *sql.DB，使 gormDB.DB() 失败。
type fakeConnPool struct{}

var errFakePool = errors.New("fake pool")

func (fakeConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errFakePool
}

func (fakeConnPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errFakePool
}

func (fakeConnPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errFakePool
}

func (fakeConnPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

type fakePoolDialector struct{ gorm.Dialector }

func (d fakePoolDialector) Initialize(db *gorm.DB) error {
	db.ConnPool = fakeConnPool{}
	return nil
}

// TestOpenGetSQLDBFailure 覆盖 gormDB.DB() 返回错误的分支。
func TestOpenGetSQLDBFailure(t *testing.T) {
	withDialector(t, fakePoolDialector{mysql.New(mysql.Config{SkipInitializeWithVersion: true})}, nil)

	_, err := Open(Config{Driver: "mysql", DSN: "ignored"}, logger.NewNop())
	if err == nil || !strings.Contains(err.Error(), "get sql.DB") {
		t.Fatalf("err = %v, want get sql.DB failure", err)
	}
}

// TestCloseSQLDBFailure 覆盖 sql.DB.Close 返回错误的分支。
func TestCloseSQLDBFailure(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	// 建立一条空闲连接，Close 时其驱动层关闭错误会被 sql.DB.Close 传播。
	if err := mockDB.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	mock.ExpectClose().WillReturnError(errors.New("close boom"))

	db := &gormDatabase{sqlDB: mockDB, log: logger.NewNop()}
	if err := db.Close(); err == nil || !strings.Contains(err.Error(), "close boom") {
		t.Fatalf("Close err = %v, want close boom", err)
	}
}
