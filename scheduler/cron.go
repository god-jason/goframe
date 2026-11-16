package scheduler

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

// Cron 是基于 robfig/cron 的定时任务实现。
// 每个任务执行时带超时控制、panic 恢复与并发跳过，避免任务堆积。
type Cron struct {
	mu      sync.Mutex
	cron    *cron.Cron
	log     logger.Logger
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	entries map[string]*entry
}

type entry struct {
	id      cron.EntryID
	spec    string
	running atomic.Bool
}

// New 创建定时任务调度器。
func New(log logger.Logger, cfg config.SchedulerConfig) (*Cron, error) {
	if log == nil {
		log = logger.NewNop()
	}
	location := time.Local
	if cfg.Location != "" {
		loc, err := time.LoadLocation(cfg.Location)
		if err != nil {
			return nil, fmt.Errorf("scheduler: load location %q: %w", cfg.Location, err)
		}
		location = loc
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return &Cron{
		cron:    cron.New(cron.WithLocation(location), cron.WithParser(parser)),
		log:     log,
		timeout: cfg.JobTimeout,
		entries: make(map[string]*entry),
	}, nil
}

// Register 注册任务。必须在 Start 之前调用。
func (s *Cron) Register(name string, spec string, fn func(ctx context.Context) error) error {
	if name == "" {
		return fmt.Errorf("scheduler: job name must not be empty")
	}
	if spec == "" {
		return fmt.Errorf("scheduler: job %q spec must not be empty", name)
	}
	if fn == nil {
		return fmt.Errorf("scheduler: job %q handler is nil", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("scheduler: cannot register job %q after start", name)
	}
	if _, exists := s.entries[name]; exists {
		return fmt.Errorf("scheduler: job %q already registered", name)
	}

	e := &entry{spec: spec}
	id, err := s.cron.AddFunc(spec, func() { s.run(name, e, fn) })
	if err != nil {
		return fmt.Errorf("scheduler: register job %q: %w", name, err)
	}
	e.id = id
	s.entries[name] = e
	s.log.Debug("scheduler job registered",
		logger.String("job", name),
		logger.String("spec", spec),
	)
	return nil
}

func (s *Cron) run(name string, e *entry, fn func(context.Context) error) {
	if !e.running.CompareAndSwap(false, true) {
		s.log.Warn("scheduler job skipped: previous run still active", logger.String("job", name))
		return
	}
	defer e.running.Store(false)
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("scheduler job panic",
				logger.String("job", name),
				logger.Any("panic", recovered),
				logger.String("stack", string(debug.Stack())),
			)
		}
	}()

	base := s.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, s.timeout)
	defer cancel()

	start := time.Now()
	if err := fn(ctx); err != nil {
		s.log.Error("scheduler job failed",
			logger.String("job", name),
			logger.Duration("elapsed", time.Since(start)),
			logger.Error(err),
		)
		return
	}
	s.log.Debug("scheduler job finished",
		logger.String("job", name),
		logger.Duration("elapsed", time.Since(start)),
	)
}

// Start 启动调度器。
func (s *Cron) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.cron.Start()
	s.started = true
	s.log.Info("scheduler started", logger.Int("jobs", len(s.entries)))
	return nil
}

// Stop 停止调度器并等待正在执行的任务结束。
func (s *Cron) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	if s.cancel != nil {
		s.cancel()
	}
	done := s.cron.Stop()
	s.mu.Unlock()

	select {
	case <-done.Done():
		s.log.Info("scheduler stopped")
		return nil
	case <-ctx.Done():
		s.log.Warn("scheduler stop timeout", logger.Int("jobs", len(s.entries)))
		return ctx.Err()
	}
}

// Jobs 返回已注册任务快照。
func (s *Cron) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]Job, 0, len(s.entries))
	for name, e := range s.entries {
		job := Job{Name: name, Spec: e.spec, Running: e.running.Load()}
		if s.started {
			if item := s.cron.Entry(e.id); item.ID != 0 {
				job.Next = item.Next
			}
		}
		jobs = append(jobs, job)
	}
	return jobs
}
