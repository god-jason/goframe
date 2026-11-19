package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hibiken/asynq"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
)

var global = struct {
	mu     sync.RWMutex
	client *asynq.Client
	server *asynq.Server
}{}

func init() {
	_ = boot.Register("taskqueue", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "cache"},
	})
}

// Startup 创建 Asynq 客户端并启动后台消费服务。未启用时直接返回。
func Startup(context.Context) error {
	current := GetConfig().normalize()
	if !current.Enabled {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	mux := asynq.NewServeMux()
	for taskType, handler := range registry.handlers {
		h := handler
		mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
			return h(ctx, t.Payload())
		})
	}

	redisOpt := current.redisOptions()
	client := asynq.NewClient(redisOpt)
	server := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:     current.Concurrency,
		Queues:          current.Queues,
		StrictPriority:  current.StrictPriority,
		ShutdownTimeout: current.ShutdownTimeout,
		Logger:          logBridge{},
		LogLevel:        asynqLogLevel(current.LogLevel),
	})
	if err := server.Start(mux); err != nil {
		_ = client.Close()
		return fmt.Errorf("taskqueue: start server: %w", err)
	}

	global.mu.Lock()
	global.client = client
	global.server = server
	global.mu.Unlock()
	registry.started = true

	logger.Get().Info("taskqueue started",
		logger.Int("concurrency", current.Concurrency),
		logger.Any("queues", current.Queues),
	)
	return nil
}

// Shutdown 优雅停止消费服务并关闭客户端连接。
func Shutdown(context.Context) error {
	global.mu.Lock()
	client, server := global.client, global.server
	global.client, global.server = nil, nil
	global.mu.Unlock()

	registry.mu.Lock()
	registry.started = false
	registry.mu.Unlock()

	var errs []error
	if server != nil {
		server.Shutdown()
	}
	if client != nil {
		if err := client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// asynqLogLevel 把配置字符串映射为 Asynq 日志级别。
func asynqLogLevel(level string) asynq.LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return asynq.DebugLevel
	case "warn", "warning":
		return asynq.WarnLevel
	case "error":
		return asynq.ErrorLevel
	case "fatal":
		return asynq.FatalLevel
	default:
		return asynq.InfoLevel
	}
}

// logBridge 把 Asynq 内部日志接入框架 logger。
type logBridge struct{}

func (logBridge) Debug(args ...any) { logger.Get().Debug(fmt.Sprint(args...)) }
func (logBridge) Info(args ...any)  { logger.Get().Info(fmt.Sprint(args...)) }
func (logBridge) Warn(args ...any)  { logger.Get().Warn(fmt.Sprint(args...)) }
func (logBridge) Error(args ...any) { logger.Get().Error(fmt.Sprint(args...)) }

// Fatal 记录错误后交由 Asynq 自行退出进程。
func (logBridge) Fatal(args ...any) { logger.Get().Error(fmt.Sprint(args...)) }
