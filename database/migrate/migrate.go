// Package migrate 提供轻量、可回滚的数据库迁移工具。
//
// 设计目标：
//   - 不依赖 GORM AutoMigrate 修改核心业务表；
//   - 每个迁移在独立事务中执行，并写入 schema_migrations 版本表；
//   - 支持 Go 函数迁移与 SQL 文件迁移；
//   - 支持 Up、Down、Status，便于在 CI/CD 与本地开发中复用；
//   - 失败立即停止并保留错误上下文，避免继续执行到不确定状态。
//
// 注意：
//   - 多实例部署时需要一个实例执行迁移，或使用数据库迁移锁（当前未内置）；
//   - SQL 文件默认按“一条语句一个文件”使用，多语句执行能力取决于数据库驱动。
package migrate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/god-jason/goframe/logger"
)

var (
	// ErrDuplicateVersion 表示版本号重复。
	ErrDuplicateVersion = errors.New("migrate: duplicate migration version")
	// ErrInvalidMigration 表示迁移定义非法。
	ErrInvalidMigration = errors.New("migrate: invalid migration")
	// ErrNoDown 表示迁移没有提供回滚函数。
	ErrNoDown = errors.New("migrate: migration has no down function")
)

const defaultTable = "schema_migrations"

// Migration 是一条数据库迁移。
// Up 必须是幂等的建表/改表/数据迁移逻辑；Down 用于回滚。
type Migration struct {
	Version int64
	Name    string
	Up      func(ctx context.Context, tx *gorm.DB) error
	Down    func(ctx context.Context, tx *gorm.DB) error
}

// Record 是版本表记录。
type Record struct {
	Version   int64     `gorm:"primaryKey;autoIncrement:false"`
	Name      string    `gorm:"size:255;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

// Status 是迁移状态。
type Status struct {
	Version   int64     `json:"version"`
	Name      string    `json:"name"`
	Applied   bool      `json:"applied"`
	AppliedAt time.Time `json:"applied_at,omitempty"`
}

// Runner 负责注册、执行与回滚迁移。
type Runner struct {
	db         *gorm.DB
	table      string
	log        logger.Logger
	migrations []Migration
}

// Option 是 Runner 选项。
type Option func(*Runner)

// WithTable 自定义版本表名称。
func WithTable(table string) Option {
	return func(r *Runner) {
		if table != "" {
			r.table = table
		}
	}
}

// WithLogger 注入日志实现。
func WithLogger(log logger.Logger) Option {
	return func(r *Runner) {
		if log != nil {
			r.log = log
		}
	}
}

// New 创建迁移执行器。
func New(db *gorm.DB, opts ...Option) (*Runner, error) {
	if db == nil {
		return nil, fmt.Errorf("migrate: database is nil")
	}
	r := &Runner{
		db:    db,
		table: defaultTable,
		log:   logger.NewNop(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Register 注册迁移。版本号必须唯一且大于 0，Up 不能为空。
func (r *Runner) Register(migrations ...Migration) error {
	for _, m := range migrations {
		if m.Version <= 0 {
			return fmt.Errorf("%w: version must be greater than 0", ErrInvalidMigration)
		}
		if m.Name == "" {
			return fmt.Errorf("%w: migration %d name must not be empty", ErrInvalidMigration, m.Version)
		}
		if m.Up == nil {
			return fmt.Errorf("%w: migration %d up function is nil", ErrInvalidMigration, m.Version)
		}
		for _, existing := range r.migrations {
			if existing.Version == m.Version {
				return fmt.Errorf("%w: %d", ErrDuplicateVersion, m.Version)
			}
		}
		r.migrations = append(r.migrations, m)
	}
	sort.Slice(r.migrations, func(i, j int) bool {
		return r.migrations[i].Version < r.migrations[j].Version
	})
	return nil
}

// Up 执行所有未应用的迁移，按版本号升序。
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	var executed []Migration
	for _, m := range r.migrations {
		if applied[m.Version] {
			continue
		}
		started := time.Now()
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Up(ctx, tx); err != nil {
				return err
			}
			return tx.Table(r.table).Create(&Record{
				Version:   m.Version,
				Name:      m.Name,
				AppliedAt: time.Now().UTC(),
			}).Error
		})
		if err != nil {
			r.log.Error("migration failed",
				logger.Int64("version", m.Version),
				logger.String("name", m.Name),
				logger.Error(err),
			)
			return executed, fmt.Errorf("migrate: apply %d_%s: %w", m.Version, m.Name, err)
		}
		r.log.Info("migration applied",
			logger.Int64("version", m.Version),
			logger.String("name", m.Name),
			logger.Duration("elapsed", time.Since(started)),
		)
		executed = append(executed, m)
	}
	return executed, nil
}

// Down 回滚最近 steps 条已应用迁移；steps<=0 表示全部回滚。
func (r *Runner) Down(ctx context.Context, steps int) ([]Migration, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	records, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	byVersion := make(map[int64]Migration, len(r.migrations))
	for _, m := range r.migrations {
		byVersion[m.Version] = m
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Version > records[j].Version
	})

	if steps <= 0 || steps > len(records) {
		steps = len(records)
	}

	var rolledBack []Migration
	for i := 0; i < steps; i++ {
		record := records[i]
		m, ok := byVersion[record.Version]
		if !ok {
			return rolledBack, fmt.Errorf("migrate: applied migration %d_%s is not registered", record.Version, record.Name)
		}
		if m.Down == nil {
			return rolledBack, fmt.Errorf("%w: %d_%s", ErrNoDown, m.Version, m.Name)
		}

		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Down(ctx, tx); err != nil {
				return err
			}
			return tx.Table(r.table).Where("version = ?", m.Version).Delete(&Record{}).Error
		})
		if err != nil {
			r.log.Error("migration rollback failed",
				logger.Int64("version", m.Version),
				logger.String("name", m.Name),
				logger.Error(err),
			)
			return rolledBack, fmt.Errorf("migrate: rollback %d_%s: %w", m.Version, m.Name, err)
		}
		r.log.Info("migration rolled back",
			logger.Int64("version", m.Version),
			logger.String("name", m.Name),
		)
		rolledBack = append(rolledBack, m)
	}
	return rolledBack, nil
}

// Status 返回所有已注册迁移及其应用状态。
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	records, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	applied := make(map[int64]Record, len(records))
	for _, record := range records {
		applied[record.Version] = record
	}

	statuses := make([]Status, 0, len(r.migrations))
	for _, m := range r.migrations {
		record, ok := applied[m.Version]
		statuses = append(statuses, Status{
			Version:   m.Version,
			Name:      m.Name,
			Applied:   ok,
			AppliedAt: record.AppliedAt,
		})
	}
	return statuses, nil
}

func (r *Runner) ensureTable(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Table(r.table).AutoMigrate(&Record{}); err != nil {
		return fmt.Errorf("migrate: create version table: %w", err)
	}
	return nil
}

func (r *Runner) records(ctx context.Context) ([]Record, error) {
	var records []Record
	if err := r.db.WithContext(ctx).Table(r.table).Order("version asc").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("migrate: load version table: %w", err)
	}
	return records, nil
}

func (r *Runner) appliedVersions(ctx context.Context) (map[int64]bool, error) {
	records, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	applied := make(map[int64]bool, len(records))
	for _, record := range records {
		applied[record.Version] = true
	}
	return applied, nil
}
