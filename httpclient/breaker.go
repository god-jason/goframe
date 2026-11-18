package httpclient

import (
	"sync"
	"time"
)

// State 是熔断器状态。
type State int

const (
	// StateClosed 表示熔断器关闭，请求正常放行。
	StateClosed State = iota
	// StateOpen 表示熔断器打开，请求快速失败。
	StateOpen
	// StateHalfOpen 表示熔断器半开，仅允许少量探测请求。
	StateHalfOpen
)

// String 返回状态名称，便于日志与测试输出。
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// BreakerOptions 是熔断器配置。
type BreakerOptions struct {
	// FailureThreshold 是连续失败多少次后打开熔断器。
	FailureThreshold int
	// OpenTimeout 是熔断器打开后经过多久进入半开状态。
	OpenTimeout time.Duration
	// SuccessThreshold 是半开状态下连续成功多少次后关闭熔断器。
	SuccessThreshold int
}

func (o BreakerOptions) withDefaults() BreakerOptions {
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = 5
	}
	if o.OpenTimeout <= 0 {
		o.OpenTimeout = 30 * time.Second
	}
	if o.SuccessThreshold <= 0 {
		o.SuccessThreshold = 2
	}
	return o
}

// Breaker 是简单的三态熔断器，线程安全。
// 半开状态同一时刻只允许一个探测请求，避免恢复瞬间压垮下游。
type Breaker struct {
	mu sync.Mutex

	opts BreakerOptions

	state State
	// failures 是闭合状态下的连续失败次数。
	failures int
	// successes 是半开状态下的连续成功次数。
	successes int
	// openedAt 是最近一次进入打开状态的时间。
	openedAt time.Time
	// probing 表示半开状态下已有探测请求在执行。
	probing bool
}

// NewBreaker 创建熔断器。零值选项使用默认值：5 次失败、30 秒打开时长、2 次成功恢复。
func NewBreaker(opts BreakerOptions) *Breaker {
	return &Breaker{opts: opts.withDefaults(), state: StateClosed}
}

// Allow 判断当前是否允许发起请求。
// 打开状态在超过 OpenTimeout 后转为半开并放行一个探测请求。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.openedAt) < b.opts.OpenTimeout {
			return false
		}
		b.state = StateHalfOpen
		b.successes = 0
		b.probing = true
		return true
	case StateHalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	default:
		return false
	}
}

// Success 记录一次成功调用。
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures = 0
	case StateHalfOpen:
		b.probing = false
		b.successes++
		if b.successes >= b.opts.SuccessThreshold {
			b.state = StateClosed
			b.failures = 0
			b.successes = 0
		}
	}
}

// Failure 记录一次失败调用。
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.opts.FailureThreshold {
			b.state = StateOpen
			b.openedAt = time.Now()
			b.failures = 0
		}
	case StateHalfOpen:
		// 探测失败立即重新打开，重新计算打开时长。
		b.state = StateOpen
		b.openedAt = time.Now()
		b.probing = false
		b.successes = 0
	}
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
