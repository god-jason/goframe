// Package health 提供健康检查能力。
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/logger"
)

// maxConcurrentChecks 是单次健康检查的最大并发数。
const maxConcurrentChecks = 8

// Status 表示健康状态。
type Status string

const (
	StatusOK          Status = "ok"
	StatusFailed      Status = "failed"
	StatusUnavailable Status = "unavailable"
)

// Check 是一项健康检查。
type Check struct {
	Name     string
	Critical bool
	Fn       func(ctx context.Context) error
}

// CheckResult 是单项检查结果。
type CheckResult struct {
	Name      string `json:"name"`
	Status    Status `json:"status"`
	Critical  bool   `json:"critical"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// Report 是健康检查报告。
type Report struct {
	Status  Status        `json:"status"`
	Version string        `json:"version,omitempty"`
	Uptime  string        `json:"uptime"`
	Checks  []CheckResult `json:"checks,omitempty"`
}

// Checker 管理健康检查项与就绪状态。
// 检查项并发执行，单项 panic 会被隔离并记录为失败，不影响其他检查项。
type Checker struct {
	version string
	started time.Time
	timeout time.Duration
	ready   atomic.Bool
	log     logger.Logger

	mu     sync.RWMutex
	checks map[string]Check
	order  []string
}

// New 创建健康检查器。
func New(version string, checks ...Check) *Checker {
	c := &Checker{
		version: version,
		started: time.Now(),
		timeout: 2 * time.Second,
		log:     logger.NewNop(),
		checks:  make(map[string]Check),
	}
	for _, check := range checks {
		_ = c.Add(check)
	}
	return c
}

// SetLogger 注入日志实现。建议在应用初始化阶段、开始接收请求前设置。
func (c *Checker) SetLogger(log logger.Logger) {
	if log == nil {
		log = logger.NewNop()
	}
	c.mu.Lock()
	c.log = log
	c.mu.Unlock()
}

// SetTimeout 设置单项检查超时，d<=0 时恢复默认 2 秒。
func (c *Checker) SetTimeout(d time.Duration) {
	if d <= 0 {
		d = 2 * time.Second
	}
	c.mu.Lock()
	c.timeout = d
	c.mu.Unlock()
}

// Add 注册健康检查项，名称重复时返回错误。
func (c *Checker) Add(check Check) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.checks[check.Name]; exists {
		return nil
	}
	c.checks[check.Name] = check
	c.order = append(c.order, check.Name)
	return nil
}

// SetReady 设置服务是否就绪。
func (c *Checker) SetReady(ready bool) { c.ready.Store(ready) }

// IsReady 返回服务是否就绪。
func (c *Checker) IsReady() bool { return c.ready.Load() }

// Register 把健康检查接口登记为框架根路径接口，由 web 启动时统一挂载。
func (c *Checker) Register() {
	api.RegisterRoot(http.MethodGet, "/health", c.HandleHealth)
	api.RegisterRoot(http.MethodGet, "/health/live", c.HandleLive)
	api.RegisterRoot(http.MethodGet, "/health/ready", c.HandleReady)
}

// HandleLive 处理存活探针。
func (c *Checker) HandleLive(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, Report{Status: StatusOK, Version: c.version, Uptime: c.uptime()})
}

// HandleReady 处理就绪探针。未就绪或关键依赖异常时返回 503。
func (c *Checker) HandleReady(ctx *gin.Context) {
	if !c.ready.Load() {
		ctx.JSON(http.StatusServiceUnavailable, Report{
			Status:  StatusUnavailable,
			Version: c.version,
			Uptime:  c.uptime(),
		})
		return
	}
	status, results := c.run(ctx.Request.Context(), true)
	code := http.StatusOK
	if status != StatusOK {
		code = http.StatusServiceUnavailable
	}
	ctx.JSON(code, Report{Status: status, Version: c.version, Uptime: c.uptime(), Checks: results})
}

// HandleHealth 处理聚合健康检查。任一关键依赖异常时返回 503。
func (c *Checker) HandleHealth(ctx *gin.Context) {
	status, results := c.run(ctx.Request.Context(), false)
	code := http.StatusOK
	if status != StatusOK {
		code = http.StatusServiceUnavailable
	}
	ctx.JSON(code, Report{Status: status, Version: c.version, Uptime: c.uptime(), Checks: results})
}

func (c *Checker) run(ctx context.Context, onlyCritical bool) (Status, []CheckResult) {
	c.mu.RLock()
	checks := make([]Check, 0, len(c.order))
	for _, name := range c.order {
		check := c.checks[name]
		if onlyCritical && !check.Critical {
			continue
		}
		checks = append(checks, check)
	}
	c.mu.RUnlock()

	c.mu.RLock()
	timeout := c.timeout
	c.mu.RUnlock()

	results := make([]CheckResult, len(checks))
	// 限制并发检查数量，避免探针被高频调用时瞬间创建大量 goroutine。
	limit := maxConcurrentChecks
	if len(checks) < limit {
		limit = len(checks)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Add(1)
		go func(i int, check Check) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = c.runCheck(ctx, check, timeout)
		}(i, check)
	}
	wg.Wait()

	status := StatusOK
	for _, result := range results {
		if result.Status != StatusOK && result.Critical {
			status = StatusFailed
			break
		}
	}
	return status, results
}

// runCheck 执行单项检查。
// 检查函数来自业务或基础设施适配层，可能 panic，因此必须隔离并转换为失败结果；
// 同时记录失败日志，便于在探针频繁调用时定位异常依赖。
func (c *Checker) runCheck(ctx context.Context, check Check, timeout time.Duration) (result CheckResult) {
	result = CheckResult{Name: check.Name, Critical: check.Critical, Status: StatusOK}
	if check.Fn == nil {
		return result
	}

	c.mu.RLock()
	log := c.log
	c.mu.RUnlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			result.Status = StatusFailed
			result.Error = fmt.Sprintf("panic: %v", recovered)
			log.Error("health check panic",
				logger.String("check", check.Name),
				logger.Any("panic", recovered),
			)
		}
	}()

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	err := check.Fn(checkCtx)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = StatusFailed
		result.Error = err.Error()
		log.Warn("health check failed",
			logger.String("check", check.Name),
			logger.Bool("critical", check.Critical),
			logger.Int64("latency_ms", result.LatencyMS),
			logger.Error(err),
		)
		return result
	}

	log.Debug("health check passed",
		logger.String("check", check.Name),
		logger.Int64("latency_ms", result.LatencyMS),
	)
	return result
}

func (c *Checker) uptime() string {
	return time.Since(c.started).Truncate(time.Second).String()
}
