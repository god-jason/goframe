package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Options 日志初始化选项。
type Options struct {
	Level  string // debug / info / warn / error
	Format string // json / console
	Output string // stdout / stderr / 文件路径
}

// ZapLogger 基于 Zap 的日志实现，支持运行时调整日志等级。
type ZapLogger struct {
	log    *zap.Logger
	level  zap.AtomicLevel
	closer *closerRef
}

// closerRef 保证底层文件只关闭一次。
type closerRef struct {
	once sync.Once
	fn   func() error
	err  error
}

func (c *closerRef) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		if c.fn != nil {
			c.err = c.fn()
		}
	})
	return c.err
}

// New 创建日志实例。
func New(opts Options) (*ZapLogger, error) {
	level, err := parseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	atomicLevel := zap.NewAtomicLevelAt(level)

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	encCfg := encoderConfig(format)

	var encoder zapcore.Encoder
	if format == "console" {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	sink, closer, err := openSink(opts.Output)
	if err != nil {
		return nil, err
	}
	core := zapcore.NewCore(encoder, sink, atomicLevel)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return &ZapLogger{log: logger, level: atomicLevel, closer: &closerRef{fn: closer}}, nil
}

func encoderConfig(format string) zapcore.EncoderConfig {
	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "timestamp"
	cfg.MessageKey = "message"
	cfg.LevelKey = "level"
	cfg.NameKey = "logger"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder
	if format == "console" {
		cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		cfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	}
	return cfg
}

func parseLevel(level string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return zapcore.InfoLevel, nil
	case "debug":
		return zapcore.DebugLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("logger: unknown level %q", level)
	}
}

func openSink(output string) (zapcore.WriteSyncer, func() error, error) {
	output = strings.TrimSpace(output)
	switch strings.ToLower(output) {
	case "", "stdout":
		return zapcore.AddSync(os.Stdout), nil, nil
	case "stderr":
		return zapcore.AddSync(os.Stderr), nil, nil
	default:
		if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
			return nil, nil, fmt.Errorf("logger: create log directory: %w", err)
		}
		file, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, nil, fmt.Errorf("logger: open log file: %w", err)
		}
		return zapcore.AddSync(file), file.Close, nil
	}
}

func (l *ZapLogger) Debug(msg string, fields ...Field) { l.log.Debug(msg, fields...) }
func (l *ZapLogger) Info(msg string, fields ...Field)  { l.log.Info(msg, fields...) }
func (l *ZapLogger) Warn(msg string, fields ...Field)  { l.log.Warn(msg, fields...) }
func (l *ZapLogger) Error(msg string, fields ...Field) { l.log.Error(msg, fields...) }

// With 返回附加固定字段的日志实例。
func (l *ZapLogger) With(fields ...Field) Logger {
	return &ZapLogger{log: l.log.With(fields...), level: l.level, closer: l.closer}
}

// Sync 刷新日志缓冲。
func (l *ZapLogger) Sync() error { return l.log.Sync() }

// Close 刷新并关闭日志文件。关闭后的实例不应继续使用。
func (l *ZapLogger) Close() error {
	_ = l.log.Sync()
	return l.closer.Close()
}

// SetLevel 运行时调整日志等级，用于配置热更新。
func (l *ZapLogger) SetLevel(level string) error {
	parsed, err := parseLevel(level)
	if err != nil {
		return err
	}
	l.level.SetLevel(parsed)
	return nil
}
