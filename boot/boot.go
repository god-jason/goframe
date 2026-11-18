// Package boot 提供按依赖关系自动启动、逆序自动释放组件的注册表。
//
// 每个组件在自己的 init() 中注册启动任务与配置，boot.Startup 按依赖拓扑顺序启动，
// 进程退出时 boot.Shutdown 逆序释放。依赖方向固定为：
//
//	config -> logger -> 其他基础组件 -> web
//
// 因此 boot 只依赖标准库，避免与 logger/config 形成导入环。
package boot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Module 是一个可启动/释放的组件。
type Module struct {
	// Depends 声明依赖的任务名，启动时先启动依赖，释放时后释放依赖。
	Depends []string
	// Startup 启动组件；为 nil 表示只参与释放顺序（例如只读资源）。
	Startup func(ctx context.Context) error
	// Shutdown 释放组件；为 nil 表示无需释放。
	Shutdown func(ctx context.Context) error
}

// Registry 是任务注册表，并发安全。
// 每个任务名唯一；启动顺序遵循注册顺序与依赖拓扑，释放顺序与启动顺序相反。
type Registry struct {
	// lifecycleMu 串行化 Startup/Shutdown，但不会在任务回调期间持有，
	// 因此在回调里查询注册表或注册新任务不会死锁。
	lifecycleMu sync.Mutex

	mu      sync.Mutex
	log     *slog.Logger
	tasks   map[string]*Module
	order   []string
	started []string
	booted  map[string]bool
}

// NewRegistry 创建不输出日志的注册表。
func NewRegistry() *Registry {
	return NewRegistryWithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// NewRegistryWithLogger 创建指定日志输出的注册表。
func NewRegistryWithLogger(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Registry{
		log:    log,
		tasks:  make(map[string]*Module),
		booted: make(map[string]bool),
	}
}

// SetLogger 替换日志实现。
func (r *Registry) SetLogger(log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	r.mu.Lock()
	r.log = log
	r.mu.Unlock()
}

// Register 注册任务。任务名不能为空，且不能重复注册。
func (r *Registry) Register(name string, task *Module) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("boot: task name must not be empty")
	}
	if task == nil {
		return fmt.Errorf("boot: task %q is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tasks[name]; exists {
		return fmt.Errorf("boot: task %q already registered", name)
	}
	r.tasks[name] = task
	r.order = append(r.order, name)
	return nil
}

// Startup 按依赖拓扑顺序启动所有任务。任一任务失败会回滚已启动的任务。
func (r *Registry) Startup(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	startedAt := time.Now()
	visiting := make(map[string]bool)
	for _, name := range r.Names() {
		if err := r.open(ctx, name, visiting); err != nil {
			r.log.Error("boot: startup failed, rolling back",
				"task", name,
				"error", err,
			)
			// 自动释放：启动失败不把半启动状态留给调用方。
			_ = r.shutdown(ctx)
			return err
		}
	}

	r.mu.Lock()
	started := len(r.started)
	log := r.log
	r.mu.Unlock()

	log.Info("boot: all tasks started",
		"tasks", started,
		"elapsed", time.Since(startedAt),
	)
	return nil
}

// open 递归启动任务及其依赖。任务回调执行期间不持有锁。
func (r *Registry) open(ctx context.Context, name string, visiting map[string]bool) error {
	r.mu.Lock()
	if r.booted[name] {
		r.mu.Unlock()
		return nil
	}
	task, exists := r.tasks[name]
	if !exists {
		r.mu.Unlock()
		return fmt.Errorf("boot: task %q not found", name)
	}
	deps := append([]string(nil), task.Depends...)
	log := r.log
	r.mu.Unlock()

	if visiting[name] {
		return fmt.Errorf("boot: dependency cycle detected at %q", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	for _, dep := range deps {
		r.mu.Lock()
		_, ok := r.tasks[dep]
		r.mu.Unlock()
		if !ok {
			log.Warn("boot: dependency not registered, skipped",
				"task", name,
				"dependency", dep,
			)
			continue
		}
		if err := r.open(ctx, dep, visiting); err != nil {
			return err
		}
	}

	stage := time.Now()
	if task.Startup != nil {
		if err := safeCall("startup", name, task.Startup, ctx, log); err != nil {
			return fmt.Errorf("boot: start %s: %w", name, err)
		}
	}

	r.mu.Lock()
	r.booted[name] = true
	r.started = append(r.started, name)
	r.mu.Unlock()

	log.Info("boot: task started",
		"task", name,
		"elapsed", time.Since(stage),
	)
	return nil
}

// Shutdown 逆序释放已启动的任务。单个任务失败不会阻断后续释放。
func (r *Registry) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	return r.shutdown(ctx)
}

// shutdown 逆序释放已启动任务。调用方必须持有 lifecycleMu。
func (r *Registry) shutdown(ctx context.Context) error {
	r.mu.Lock()
	started := r.started
	r.started = nil
	for _, name := range started {
		delete(r.booted, name)
	}
	log := r.log
	r.mu.Unlock()

	if len(started) == 0 {
		return nil
	}

	startedAt := time.Now()
	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		name := started[i]

		r.mu.Lock()
		task := r.tasks[name]
		r.mu.Unlock()
		if task == nil || task.Shutdown == nil {
			continue
		}

		stage := time.Now()
		if err := safeCall("shutdown", name, task.Shutdown, ctx, log); err != nil {
			errs = append(errs, fmt.Errorf("boot: stop %s: %w", name, err))
			log.Warn("boot: task stop failed",
				"task", name,
				"error", err,
			)
			continue
		}
		log.Info("boot: task stopped",
			"task", name,
			"elapsed", time.Since(stage),
		)
	}

	log.Info("boot: all tasks stopped",
		"elapsed", time.Since(startedAt),
		"errors", len(errs),
	)
	return errors.Join(errs...)
}

// Names 返回按注册顺序排列的任务名。
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	return names
}

// Started 返回按启动顺序排列的已启动任务名。
func (r *Registry) Started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, len(r.started))
	copy(names, r.started)
	return names
}

// safeCall 隔离任务回调中的 panic，并统一转换为错误。
func safeCall(action, name string, fn func(context.Context) error, ctx context.Context, log *slog.Logger) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("boot: task panic",
				"task", name,
				"action", action,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return fn(ctx)
}

// 包级默认注册表，便于组件在 init() 中自动注册。
var defaultRegistry = NewRegistry()

// Default 返回包级默认注册表。
func Default() *Registry { return defaultRegistry }

// Register 向默认注册表注册任务。
func Register(name string, task *Module) error { return defaultRegistry.Register(name, task) }

// Startup 启动默认注册表中的所有任务。
func Startup(ctx context.Context) error { return defaultRegistry.Startup(ctx) }

// Shutdown 逆序释放默认注册表中已启动的任务。
func Shutdown(ctx context.Context) error { return defaultRegistry.Shutdown(ctx) }
