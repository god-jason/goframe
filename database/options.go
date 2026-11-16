package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/logger"
)

// gormLogger 将 GORM 日志输出到框架日志组件。
type gormLogger struct {
	log           logger.Logger
	level         gormlogger.LogLevel
	slowThreshold time.Duration
}

func newGormLogger(log logger.Logger, level string, slowThreshold time.Duration) gormlogger.Interface {
	logLevel := gormlogger.Warn
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		logLevel = gormlogger.Silent
	case "error":
		logLevel = gormlogger.Error
	case "info":
		logLevel = gormlogger.Info
	case "", "warn":
		logLevel = gormlogger.Warn
	}
	if slowThreshold <= 0 {
		slowThreshold = 500 * time.Millisecond
	}
	return &gormLogger{log: log, level: logLevel, slowThreshold: slowThreshold}
}

func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

func (l *gormLogger) Info(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Info {
		l.log.Info(fmt.Sprintf(msg, args...))
	}
}

func (l *gormLogger) Warn(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Warn {
		l.log.Warn(fmt.Sprintf(msg, args...))
	}
}

func (l *gormLogger) Error(_ context.Context, msg string, args ...any) {
	if l.level >= gormlogger.Error {
		l.log.Error(fmt.Sprintf(msg, args...))
	}
}

func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	fields := []logger.Field{
		logger.Duration("elapsed", elapsed),
		logger.Int64("rows", rows),
		logger.String("sql", sql),
	}

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		l.log.Error("database query failed", append(fields, logger.Error(err))...)
	case elapsed > l.slowThreshold:
		l.log.Warn("database slow query", fields...)
	case l.level >= gormlogger.Info:
		l.log.Debug("database query", fields...)
	}
	_ = ctx
}
