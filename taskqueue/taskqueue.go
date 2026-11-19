// Package taskqueue 基于 Asynq（Redis）提供分布式异步任务队列，
// 支持并发量、队列权重、重试次数、延迟执行等配置，
// 业务组件通过 Register 注册处理器、Enqueue 投递任务。
//
// 队列、并发等参数来自 taskqueue 配置段，可随配置文件热更新；
// 处理器必须在 boot 启动前注册（通常在包 init 中）。
package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
)

// ErrNotEnabled 表示任务队列未启用或尚未启动。
var ErrNotEnabled = errors.New("taskqueue: not enabled")

// Handler 是任务处理器，payload 为入队时的原始字节。
type Handler func(ctx context.Context, payload []byte) error

var registry = struct {
	mu       sync.Mutex
	handlers map[string]Handler
	started  bool
}{handlers: map[string]Handler{}}

// Register 注册任务类型对应的处理器。
// 必须在 boot 启动前调用；类型为空、处理器为 nil、重复注册或启动后注册都会 panic。
func Register(taskType string, h Handler) {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		panic("taskqueue: task type must not be empty")
	}
	if h == nil {
		panic(fmt.Sprintf("taskqueue: handler for %q must not be nil", taskType))
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.started {
		panic(fmt.Sprintf("taskqueue: register %q after server started", taskType))
	}
	if _, dup := registry.handlers[taskType]; dup {
		panic(fmt.Sprintf("taskqueue: task type %q already registered", taskType))
	}
	registry.handlers[taskType] = h
}

// Enabled 报告任务队列是否已启动并可用。
func Enabled() bool {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.client != nil
}

// enqueueOptions 汇总单次入队参数。
type enqueueOptions struct {
	queue    string
	maxRetry int
	timeout  time.Duration
	delay    time.Duration
	unique   time.Duration
}

// Option 是入队选项。
type Option func(*enqueueOptions)

// Queue 指定目标队列，队列必须存在于配置的 queues 权重表中。
func Queue(name string) Option { return func(o *enqueueOptions) { o.queue = name } }

// MaxRetry 覆盖默认重试次数。
func MaxRetry(n int) Option { return func(o *enqueueOptions) { o.maxRetry = n } }

// Timeout 覆盖单任务执行超时；d <= 0 表示不设置超时。
func Timeout(d time.Duration) Option { return func(o *enqueueOptions) { o.timeout = d } }

// Delay 延迟指定时长后执行。
func Delay(d time.Duration) Option { return func(o *enqueueOptions) { o.delay = d } }

// Unique 在 ttl 窗口内对同类型同 payload 任务去重。
func Unique(ttl time.Duration) Option { return func(o *enqueueOptions) { o.unique = ttl } }

// Enqueue 投递任务。任务队列未启用时返回 ErrNotEnabled，由调用方决定回退行为。
func Enqueue(ctx context.Context, taskType string, payload []byte, opts ...Option) error {
	if strings.TrimSpace(taskType) == "" {
		return errors.New("taskqueue: task type must not be empty")
	}
	current := GetConfig().normalize()
	o := enqueueOptions{queue: "default", maxRetry: current.MaxRetry, timeout: current.TaskTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.queue == "" {
		o.queue = "default"
	}

	global.mu.RLock()
	client := global.client
	global.mu.RUnlock()
	if client == nil {
		return ErrNotEnabled
	}

	taskOpts := []asynq.Option{asynq.Queue(o.queue), asynq.MaxRetry(o.maxRetry)}
	if o.timeout > 0 {
		taskOpts = append(taskOpts, asynq.Timeout(o.timeout))
	}
	if o.delay > 0 {
		taskOpts = append(taskOpts, asynq.ProcessIn(o.delay))
	}
	if o.unique > 0 {
		taskOpts = append(taskOpts, asynq.Unique(o.unique))
	}
	if _, err := client.EnqueueContext(ctx, asynq.NewTask(taskType, payload), taskOpts...); err != nil {
		return fmt.Errorf("taskqueue: enqueue %s: %w", taskType, err)
	}
	return nil
}
