// Package database 封装 GORM 数据库访问与事务管理。
package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

// Database 是框架统一的数据库接口。业务模块通过 Repository 接口访问数据，
// 不直接依赖 GORM。
type Database interface {
	// DB 返回底层 GORM 实例，仅供基础设施与 Repository 实现使用。
	DB() *gorm.DB
	// Ping 检查数据库连通性。
	Ping(ctx context.Context) error
	// Transaction 在事务中执行 fn，fn 内的多个 Repository 操作必须共用 tx。
	Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error
	// Stats 返回连接池统计信息。
	Stats() sql.DBStats
	// Close 关闭数据库连接。
	Close() error
}

type gormDatabase struct {
	gorm  *gorm.DB
	sqlDB *sql.DB
	log   logger.Logger
}

// Open 根据配置创建数据库连接。
func Open(cfg config.DatabaseConfig, log logger.Logger) (Database, error) {
	if log == nil {
		log = logger.NewNop()
	}

	log.Debug("database connecting", logger.String("driver", cfg.Driver))
	var dialector gorm.Dialector
	switch strings.ToLower(cfg.Driver) {
	case "mysql":
		dialector = mysqlDialector(cfg.DSN)
	case "postgres":
		dialector = postgresDialector(cfg.DSN)
	default:
		return nil, fmt.Errorf("database: unsupported driver %q", cfg.Driver)
	}

	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger:  newGormLogger(log, cfg.LogLevel, cfg.SlowThreshold),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", cfg.Driver, err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("database: get sql.DB: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	db := &gormDatabase{gorm: gormDB, sqlDB: sqlDB, log: log}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err != nil {
		log.Error("database ping failed",
			logger.String("driver", cfg.Driver),
			logger.Error(err),
		)
		_ = sqlDB.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	log.Info("database connected",
		logger.String("driver", cfg.Driver),
		logger.Int("max_open_conns", cfg.MaxOpenConns),
		logger.Int("max_idle_conns", cfg.MaxIdleConns),
		logger.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
	)
	return db, nil
}

// DB 返回底层 GORM 实例。
func (d *gormDatabase) DB() *gorm.DB { return d.gorm }

// Ping 检查数据库连通性。
func (d *gormDatabase) Ping(ctx context.Context) error {
	if d.sqlDB == nil {
		return fmt.Errorf("database: not initialized")
	}
	return d.sqlDB.PingContext(ctx)
}

// Transaction 在事务中执行 fn。
func (d *gormDatabase) Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if fn == nil {
		return fmt.Errorf("database: transaction callback is nil")
	}
	return d.gorm.WithContext(ctx).Transaction(fn)
}

// Stats 返回连接池统计信息。
func (d *gormDatabase) Stats() sql.DBStats {
	if d.sqlDB == nil {
		return sql.DBStats{}
	}
	return d.sqlDB.Stats()
}

// Close 关闭数据库连接。
func (d *gormDatabase) Close() error {
	if d.sqlDB == nil {
		return nil
	}
	d.log.Info("database closing")
	if err := d.sqlDB.Close(); err != nil {
		d.log.Warn("database close failed", logger.Error(err))
		return err
	}
	d.log.Info("database closed")
	return nil
}
