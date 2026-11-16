package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// 日志默认输出与滚动参数。
const (
	// defaultOutput 是未指定输出时的默认日志文件。
	defaultOutput = "logs/app.log"
	// defaultRotateMaxSize 是单个日志文件的默认上限（100MB）。
	defaultRotateMaxSize int64 = 100 << 20
	// defaultRotateMaxAge 是旧日志的默认保留天数。
	defaultRotateMaxAge = 30
	// defaultRotateMaxBackups 是旧日志的默认保留数量。
	defaultRotateMaxBackups = 10
)

// Options 日志初始化选项。
type Options struct {
	Level  string        // debug / info / warn / error
	Format string        // json / console
	Output string        // stdout / stderr / 文件路径；为空时写入 logs/app.log
	Rotate RotateOptions // 文件输出滚动配置
}

// RotateOptions 是日志文件滚动配置。
// 文件输出默认开启滚动；写入 stdout/stderr 时忽略该配置。
type RotateOptions struct {
	// Disabled 显式关闭滚动，退化为直接追加写入单个文件。
	Disabled bool
	// MaxSize 是单个日志文件上限（字节），最小按 1MB 计算；<=0 使用默认 100MB。
	MaxSize int64
	// MaxAge 是旧日志保留天数；<=0 使用默认 30 天。
	MaxAge int
	// MaxBackups 是旧日志保留数量；<=0 使用默认 10 个。
	MaxBackups int
	// Compress 是否压缩旧日志（.gz）。
	Compress bool
	// LocalTime 备份文件时间是否使用本地时区。
	LocalTime bool
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

	output := strings.TrimSpace(opts.Output)
	if output == "" {
		output = defaultOutput
	}
	sink, closer, err := openSink(output, opts.Rotate)
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

// openSink 根据输出目标创建写入器。
// 文件输出默认使用 lumberjack 滚动，避免单个日志文件无限增长。
func openSink(output string, rotate RotateOptions) (zapcore.WriteSyncer, func() error, error) {
	switch strings.ToLower(output) {
	case "stdout":
		return zapcore.AddSync(os.Stdout), nil, nil
	case "stderr":
		return zapcore.AddSync(os.Stderr), nil, nil
	default:
		if dir := filepath.Dir(output); dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, nil, fmt.Errorf("logger: create log directory: %w", err)
			}
		}
		if rotate.Disabled {
			file, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
			if err != nil {
				return nil, nil, fmt.Errorf("logger: open log file: %w", err)
			}
			return zapcore.AddSync(file), file.Close, nil
		}

		// lumberjack 在首次写入时才创建文件；这里预创建，保证即使没有日志输出
		// 也能看到日志文件，便于运维确认日志路径已生效。
		file, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, nil, fmt.Errorf("logger: open log file: %w", err)
		}
		_ = file.Close()

		rolling := &lumberjack.Logger{
			Filename:   output,
			MaxSize:    rotateMaxSizeMB(rotate.MaxSize),
			MaxAge:     positiveOr(rotate.MaxAge, defaultRotateMaxAge),
			MaxBackups: positiveOr(rotate.MaxBackups, defaultRotateMaxBackups),
			Compress:   rotate.Compress,
			LocalTime:  rotate.LocalTime,
		}
		return zapcore.AddSync(rolling), rolling.Close, nil
	}
}

// rotateMaxSizeMB 把字节数转换为 lumberjack 需要的 MB，最小 1MB。
func rotateMaxSizeMB(size int64) int {
	if size <= 0 {
		size = defaultRotateMaxSize
	}
	mb := int(size / (1 << 20))
	if mb <= 0 {
		mb = 1
	}
	return mb
}

// positiveOr 返回 value，value <= 0 时返回默认值。
func positiveOr(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (l *ZapLogger) Debug(msg string, fields ...Field) { l.log.Debug(msg, fields...) }
func (l *ZapLogger) Info(msg string, fields ...Field)  { l.log.Info(msg, fields...) }
func (l *ZapLogger) Warn(msg string, fields ...Field)  { l.log.Warn(msg, fields...) }
func (l *ZapLogger) Error(msg string, fields ...Field) { l.log.Error(msg, fields...) }

// With 返回附加固定字段的日志实例。
// 子实例不拥有底层文件，关闭子实例只刷新缓冲，不会关闭根日志文件。
func (l *ZapLogger) With(fields ...Field) Logger {
	return &ZapLogger{log: l.log.With(fields...), level: l.level}
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
