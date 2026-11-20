package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// defaultShutdownTimeout 是 Run 释放阶段的默认超时，与 web 服务优雅停机上限一致量级。
const defaultShutdownTimeout = 30 * time.Second

// WithSignal 返回监听 SIGINT/SIGTERM 的派生 ctx 与释放函数，
// 语义等同 signal.NotifyContext，供业务自行编排启动与退出流程。
// ctx 为 nil 时使用 context.Background()。
func WithSignal(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

// RunOption 配置 Run 行为。
type RunOption func(*runConfig)

type runConfig struct {
	shutdownTimeout time.Duration
}

// WithShutdownTimeout 覆盖 Run 释放阶段的超时；d <= 0 时保持默认 30s。
func WithShutdownTimeout(d time.Duration) RunOption {
	return func(c *runConfig) {
		if d > 0 {
			c.shutdownTimeout = d
		}
	}
}

// Run 执行标准进程生命周期：监听 SIGINT/SIGTERM 后按依赖拓扑启动全部任务，
// 运行 serve 并阻塞（serve 为 nil 时仅等待退出信号或 ctx 取消）；
// serve 返回、ctx 取消或收到退出信号后，使用独立的超时 ctx 逆序释放已启动任务。
//
// 返回 Startup、serve 与 Shutdown 阶段错误的合并结果；
// 启动失败时不会执行 serve，Startup 内部已自动回滚，Run 不再重复释放。
// serve 通常传入阻塞的 HTTP 服务（如 web.Run），其收到的 ctx 在退出信号到来时取消。
func (r *Registry) Run(ctx context.Context, serve func(context.Context) error, opts ...RunOption) error {
	cfg := runConfig{shutdownTimeout: defaultShutdownTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	sigCtx, stop := WithSignal(ctx)
	defer stop()

	if err := r.Startup(sigCtx); err != nil {
		return err
	}

	var errs []error
	if serve != nil {
		if err := serve(sigCtx); err != nil {
			errs = append(errs, fmt.Errorf("boot: serve: %w", err))
		}
	} else {
		<-sigCtx.Done()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	if err := r.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Run 在默认注册表上执行 Registry.Run。
func Run(ctx context.Context, serve func(context.Context) error, opts ...RunOption) error {
	return defaultRegistry.Run(ctx, serve, opts...)
}
