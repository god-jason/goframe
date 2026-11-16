// Package logger 定义框架日志抽象与 Zap 实现。
package logger

import "go.uber.org/zap"

// Logger 是框架统一的日志接口，业务代码只依赖该接口。
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	With(fields ...Field) Logger
	Sync() error
}

// Field 是日志字段。
type Field = zap.Field

// 常用字段构造器，避免业务代码直接依赖 Zap。
var (
	String   = zap.String
	Strings  = zap.Strings
	Int      = zap.Int
	Int64    = zap.Int64
	Float64  = zap.Float64
	Bool     = zap.Bool
	Duration = zap.Duration
	Time     = zap.Time
	Any      = zap.Any
	Error    = zap.Error
	Stack    = zap.Stack
)

// NewNop 返回不做任何输出的日志实现，主要用于测试。
func NewNop() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Debug(string, ...Field) {}
func (nopLogger) Info(string, ...Field)  {}
func (nopLogger) Warn(string, ...Field)  {}
func (nopLogger) Error(string, ...Field) {}
func (n nopLogger) With(...Field) Logger { return n }
func (nopLogger) Sync() error            { return nil }
