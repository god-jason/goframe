// Package pool 提供带生命周期的有界协程池，用于承载框架与业务的后台任务。
//
// 设计目标：
//   - 有界并发：固定 worker 数量，避免每来一个任务就创建一个 goroutine 导致资源失控；
//   - 有界队列：队列满时可阻塞等待（Submit）或快速失败（TrySubmit），由调用方选择背压策略；
//   - 崩溃隔离：单个任务 panic 不会终止进程，也不会带走 worker，其余任务继续执行；
//   - 优雅退出：Stop 先拒绝新任务，再排空队列；超时后取消池上下文，避免无限等待；
//   - 可观测：统计运行中、排队中、完成、失败、拒绝与 panic 数量。
//
// 典型用法：
//
//	p := pool.New(pool.Options{Name: "notify", Workers: 8, QueueSize: 1024, Logger: log})
//	if err := p.Start(ctx); err != nil { ... }
//	if err := p.Submit(ctx, func(ctx context.Context) error { ... }); err != nil { ... }
//	if err := p.Stop(stopCtx); err != nil { ... }
package pool

import (
	"context"
	stderrors "errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/god-jason/goframe/logger"
)

// 预定义错误。调用方可用 errors.Is 判断具体失败原因。
var (
	// ErrNilTask 表示提交了空任务。
	ErrNilTask = stderrors.New("pool: task must not be nil")
	// ErrNotStarted 表示协程池尚未调用 Start。
	ErrNotStarted = stderrors.New("pool: pool is not started")
	// ErrClosed 表示协程池已经关闭，不能再提交任务。
	ErrClosed = stderrors.New("pool: pool is closed")
	// ErrQueueFull 表示队列已满（仅 TrySubmit 返回）。
	ErrQueueFull = stderrors.New("pool: task queue is full")
)

const (
	// defaultQueueSize 是未显式配置时的默认队列容量。
	defaultQueueSize = 1024
	// maxWorkers 是 worker 数量上限，防止错误配置一次性创建过多 goroutine。
	maxWorkers = 1024
)

// Task 是协程池执行的任务。实现方必须尊重 ctx 取消信号，
// 否则 Stop 超时后只能取消池上下文，无法强制终止已经失控的任务。
type Task func(ctx context.Context) error

// Options 是协程池构造参数。
type Options struct {
	// Name 用于日志与监控标识，默认 "pool"。
	Name string
	// Workers 是并发 worker 数量；<=0 时按 CPU 核数的 2 倍自动计算。
	Workers int
	// QueueSize 是等待队列容量；<=0 时使用 1024。
	QueueSize int
	// Logger 用于输出启动、停止、任务失败与 panic 日志。
	Logger logger.Logger
	// OnError 在任务返回错误或 panic 后被调用，便于接入监控/告警。
	// 该回调自身 panic 会被捕获，不影响 worker。
	OnError func(err error)
}

// Stats 是协程池运行状态快照。
type Stats struct {
	Name      string `json:"name"`
	Workers   int    `json:"workers"`
	QueueSize int    `json:"queue_size"`
	Queued    int    `json:"queued"`
	Running   int64  `json:"running"`
	Completed uint64 `json:"completed"`
	Failed    uint64 `json:"failed"`
	Rejected  uint64 `json:"rejected"`
	Panics    uint64 `json:"panics"`
}

// job 是队列中的任务单元，携带提交方上下文，便于任务感知调用方取消。
type job struct {
	ctx  context.Context
	task Task
}

// Pool 是有界协程池实现。
//
// 生命周期状态：未启动 -> 运行中 -> 已关闭。Start 可重复调用，Stop 幂等。
// 并发安全：Submit/TrySubmit/Stats 可并发调用；Start/Stop 由内部互斥锁串行化。
type Pool struct {
	name    string
	workers int
	log     logger.Logger
	onError func(error)

	jobs   chan job      // 任务队列
	space  chan struct{} // 队列腾出空位的通知（容量 1，避免惊群）
	done   chan struct{} // 关闭信号，Submit 等待时立即返回
	stopCh chan struct{} // 停止信号，worker 收到后排空队列并退出

	started atomic.Bool
	stopped atomic.Bool

	lifecycleMu sync.Mutex // 串行化 Start/Stop
	submitMu    sync.Mutex // 保证 Stop 关闭 done 时不会与 Submit 入队产生竞态

	runCtx context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running   atomic.Int64
	completed atomic.Uint64
	failed    atomic.Uint64
	rejected  atomic.Uint64
	panics    atomic.Uint64
}

// New 创建协程池。New 只构造对象，不启动 goroutine；必须显式调用 Start。
func New(opts Options) *Pool {
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU() * 2
	}
	if opts.Workers > maxWorkers {
		opts.Workers = maxWorkers
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.Logger == nil {
		opts.Logger = logger.NewNop()
	}
	if opts.Name == "" {
		opts.Name = "pool"
	}

	return &Pool{
		name:    opts.Name,
		workers: opts.Workers,
		log:     opts.Logger,
		onError: opts.OnError,
		jobs:    make(chan job, opts.QueueSize),
		space:   make(chan struct{}, 1),
		done:    make(chan struct{}),
		stopCh:  make(chan struct{}),
	}
}

// Name 返回协程池名称。
func (p *Pool) Name() string { return p.name }

// Workers 返回 worker 数量。
func (p *Pool) Workers() int { return p.workers }

// IsRunning 返回协程池是否已启动且未关闭。
func (p *Pool) IsRunning() bool { return p.started.Load() && !p.stopped.Load() }

// Start 启动 worker。ctx 用于控制池的生命周期：ctx 取消后所有 worker 退出。
// 重复调用 Start 是安全的；已关闭的池返回 ErrClosed。
func (p *Pool) Start(ctx context.Context) error {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	if p.stopped.Load() {
		return ErrClosed
	}
	if p.started.Load() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runCtx, cancel := context.WithCancel(ctx)
	p.runCtx = runCtx
	p.cancel = cancel
	p.started.Store(true)

	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i + 1)
	}
	// 父 context 取消时 worker 会退出；同步把池标记为已关闭，
	// 避免 Submit 继续入队后在满队列上永久阻塞。
	go func() {
		<-runCtx.Done()
		p.markStopped()
	}()
	p.log.Info("worker pool started",
		logger.String("pool", p.name),
		logger.Int("workers", p.workers),
		logger.Int("queue_size", cap(p.jobs)),
	)
	return nil
}

// Submit 提交任务。队列满时会阻塞等待空位，直到成功入队、ctx 取消或池关闭。
// 该方法保证任务不会因为 Stop 与入队并发而丢失。
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if task == nil {
		return ErrNilTask
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		// 在持有 submitMu 时完成“状态检查 + 入队”，Stop 在同一把锁内
		// 设置 stopped 并关闭 done，因此不会出现入队到已关闭池的竞态。
		p.submitMu.Lock()
		if p.stopped.Load() {
			p.submitMu.Unlock()
			return ErrClosed
		}
		if !p.started.Load() {
			p.submitMu.Unlock()
			return ErrNotStarted
		}
		select {
		case p.jobs <- job{ctx: ctx, task: task}:
			p.submitMu.Unlock()
			return nil
		default:
			p.submitMu.Unlock()
		}

		// 队列已满，等待空位/取消/关闭，避免忙等。
		select {
		case <-ctx.Done():
			p.rejected.Add(1)
			return ctx.Err()
		case <-p.done:
			p.rejected.Add(1)
			return ErrClosed
		case <-p.space:
		}
	}
}

// TrySubmit 非阻塞提交任务。队列满时立即返回 ErrQueueFull，适合可丢弃的采集任务。
func (p *Pool) TrySubmit(ctx context.Context, task Task) error {
	if task == nil {
		return ErrNilTask
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.submitMu.Lock()
	defer p.submitMu.Unlock()

	if p.stopped.Load() {
		p.rejected.Add(1)
		return ErrClosed
	}
	if !p.started.Load() {
		p.rejected.Add(1)
		return ErrNotStarted
	}
	select {
	case p.jobs <- job{ctx: ctx, task: task}:
		return nil
	default:
		p.rejected.Add(1)
		return ErrQueueFull
	}
}

// Stop 优雅关闭：先拒绝新任务，再等待 worker 排空队列。
// 超时后取消池上下文并返回 ctx.Err()；已经在执行且不响应 ctx 的任务无法被强制终止。
func (p *Pool) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cancel, started := p.markStopped()
	if !started && !p.started.Load() {
		return nil
	}

	// 等待所有 worker 排空队列后退出；超时则取消任务上下文。
	waited := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		if cancel != nil {
			cancel()
		}
		p.log.Info("worker pool stopped",
			logger.String("pool", p.name),
			logger.Uint64("completed", p.completed.Load()),
			logger.Uint64("failed", p.failed.Load()),
			logger.Uint64("panics", p.panics.Load()),
		)
		return nil
	case <-ctx.Done():
		// 先取消池上下文，让正在执行的任务感知取消；再给一个很短的收尾窗口，
		// 避免 worker 仍在写日志/释放资源时就关闭下游依赖。
		if cancel != nil {
			cancel()
		}
		select {
		case <-waited:
		case <-time.After(time.Second):
		}
		p.log.Warn("worker pool stop timeout, cancelling running tasks",
			logger.String("pool", p.name),
			logger.Int64("running", p.running.Load()),
			logger.Int("queued", len(p.jobs)),
		)
		return ctx.Err()
	}
}

// markStopped 把池切换为关闭状态，并返回取消函数与是否真正执行了状态切换。
// 通过 stopped 标志保证 stopCh/done 只关闭一次，供 Stop 与父 context 监听共用。
func (p *Pool) markStopped() (context.CancelFunc, bool) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	if p.stopped.Load() {
		return nil, false
	}
	p.stopped.Store(true)

	// 与 Submit 的“检查 + 入队”互斥：关闭 done 后不会再有新任务进入队列。
	p.submitMu.Lock()
	close(p.done)
	p.submitMu.Unlock()

	if !p.started.Load() {
		return nil, false
	}
	close(p.stopCh)
	return p.cancel, true
}

// Stats 返回当前统计快照。
func (p *Pool) Stats() Stats {
	return Stats{
		Name:      p.name,
		Workers:   p.workers,
		QueueSize: cap(p.jobs),
		Queued:    len(p.jobs),
		Running:   p.running.Load(),
		Completed: p.completed.Load(),
		Failed:    p.failed.Load(),
		Rejected:  p.rejected.Load(),
		Panics:    p.panics.Load(),
	}
}

// worker 是 worker 主循环。
// 收到 stopCh 后进入排空模式：继续消费队列中剩余任务，队列为空才退出。
func (p *Pool) worker(id int) {
	defer p.wg.Done()
	p.log.Debug("worker started",
		logger.String("pool", p.name),
		logger.Int("worker", id),
	)

	for {
		select {
		case <-p.runCtx.Done():
			return
		case j := <-p.jobs:
			p.signalSpace()
			p.execute(j)
		case <-p.stopCh:
			p.drain()
			p.log.Debug("worker stopped",
				logger.String("pool", p.name),
				logger.Int("worker", id),
			)
			return
		}
	}
}

// drain 排空队列中的剩余任务（停止阶段使用）。
func (p *Pool) drain() {
	for {
		select {
		case j := <-p.jobs:
			p.signalSpace()
			p.execute(j)
		default:
			return
		}
	}
}

// execute 执行单个任务：统计耗时、记录日志、隔离 panic。
// 任务上下文由提交方 ctx 派生，同时监听池上下文：Stop 超时后取消池上下文
// 会同步取消所有正在执行的任务，避免 worker 永久阻塞。
func (p *Pool) execute(j job) {
	p.running.Add(1)
	start := time.Now()

	taskCtx, cancel := context.WithCancel(j.ctx)
	stopLink := context.AfterFunc(p.runCtx, cancel)
	defer func() {
		stopLink()
		cancel()
	}()

	defer func() {
		p.running.Add(-1)
		if recovered := recover(); recovered != nil {
			p.panics.Add(1)
			p.failed.Add(1)
			err := fmt.Errorf("pool: task panic: %v", recovered)
			p.log.Error("worker task panic",
				logger.String("pool", p.name),
				logger.Duration("elapsed", time.Since(start)),
				logger.Any("panic", recovered),
				logger.String("stack", string(debug.Stack())),
			)
			p.report(err)
		}
	}()

	if err := j.task(taskCtx); err != nil {
		p.failed.Add(1)
		p.log.Warn("worker task failed",
			logger.String("pool", p.name),
			logger.Duration("elapsed", time.Since(start)),
			logger.Error(err),
		)
		p.report(err)
		return
	}

	p.completed.Add(1)
	p.log.Debug("worker task completed",
		logger.String("pool", p.name),
		logger.Duration("elapsed", time.Since(start)),
	)
}

// signalSpace 通知一个等待中的 Submit 队列已有空位。非阻塞，避免惊群。
func (p *Pool) signalSpace() {
	select {
	case p.space <- struct{}{}:
	default:
	}
}

// report 调用错误回调，并确保回调自身的 panic 不会影响 worker。
func (p *Pool) report(err error) {
	if p.onError == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			p.log.Error("pool error handler panic",
				logger.String("pool", p.name),
				logger.Any("panic", recovered),
			)
		}
	}()
	p.onError(err)
}
