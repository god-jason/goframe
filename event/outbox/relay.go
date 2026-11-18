package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/god-jason/goframe/event"
)

// maxLastErrorLen 限制错误信息长度，避免异常堆栈撑爆数据库列。
const maxLastErrorLen = 2048

// Options 是 Relay 配置。
type Options struct {
	// Interval 是后台轮询间隔，默认 1 秒。
	Interval time.Duration
	// BatchSize 是单次最多领取的消息数，默认 100。
	BatchSize int
	// MaxAttempts 是最大投递次数，达到后置为 failed，默认 10。
	MaxAttempts int
	// BaseDelay 是首次失败后的重试延迟，默认 1 秒。
	BaseDelay time.Duration
	// MaxDelay 是指数退避的延迟上限，默认 1 分钟。
	MaxDelay time.Duration
	// LeaseTimeout 是领取后的租约时长，超时未完成的消息可被重新领取，默认 1 分钟。
	LeaseTimeout time.Duration
}

// DefaultOptions 返回 Relay 默认配置。
func DefaultOptions() Options {
	return Options{
		Interval:     time.Second,
		BatchSize:    100,
		MaxAttempts:  10,
		BaseDelay:    time.Second,
		MaxDelay:     time.Minute,
		LeaseTimeout: time.Minute,
	}
}

// normalize 补全非法或零值配置，保证后台循环安全运行。
func (o Options) normalize() Options {
	defaults := DefaultOptions()
	if o.Interval <= 0 {
		o.Interval = defaults.Interval
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaults.BatchSize
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaults.MaxAttempts
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = defaults.BaseDelay
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = defaults.MaxDelay
	}
	if o.LeaseTimeout <= 0 {
		o.LeaseTimeout = defaults.LeaseTimeout
	}
	return o
}

// Relay 从数据库领取 outbox 消息并投递到 EventBus。
type Relay struct {
	db   *gorm.DB
	bus  event.EventBus
	opts Options

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New 创建 Relay。db 与 bus 在 Start 时校验，便于先构造再注入依赖。
func New(db *gorm.DB, bus event.EventBus, opts Options) *Relay {
	return &Relay{db: db, bus: bus, opts: opts.normalize()}
}

// AutoMigrate 创建 outbox 表与索引。
func (r *Relay) AutoMigrate() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("outbox: database is nil")
	}
	if err := r.db.AutoMigrate(&Message{}); err != nil {
		return fmt.Errorf("outbox: auto migrate: %w", err)
	}
	return nil
}

// Start 启动后台投递循环。重复启动返回错误，ctx 取消后循环自动退出。
func (r *Relay) Start(ctx context.Context) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("outbox: database is nil")
	}
	if r.bus == nil {
		return fmt.Errorf("outbox: event bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return fmt.Errorf("outbox: relay already started")
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	go r.loop(runCtx)
	return nil
}

// Stop 停止后台投递循环并等待其退出；ctx 用于限制等待时间。
func (r *Relay) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("outbox: stop: %w", ctx.Err())
	}
}

// loop 立即执行一次投递，然后按 Interval 周期执行。
func (r *Relay) loop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()

	for {
		if err := r.dispatch(ctx); err != nil && ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// dispatch 领取一批消息并逐条投递。单条失败会记录状态并继续处理后续消息。
func (r *Relay) dispatch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	messages, err := r.claim(ctx)
	if err != nil {
		return fmt.Errorf("outbox: claim: %w", err)
	}
	for i := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.publish(ctx, &messages[i])
	}
	return nil
}

// claim 在一个事务内领取待投递消息并标记为 publishing。
// mysql/postgres 使用 FOR UPDATE SKIP LOCKED 避免多个 Relay 重复领取；
// sqlite 不支持行级锁，退化为普通查询。
func (r *Relay) claim(ctx context.Context) ([]Message, error) {
	var messages []Message
	now := time.Now().UTC()
	leaseUntil := now.Add(r.opts.LeaseTimeout)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&Message{}).
			Where("status IN ? AND next_attempt_at <= ?",
				[]string{string(StatusPending), string(StatusPublishing)}, now).
			Order("next_attempt_at ASC").
			Limit(r.opts.BatchSize)
		if supportsSkipLocked(tx) {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := query.Find(&messages).Error; err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}

		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		return tx.Model(&Message{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":          string(StatusPublishing),
				"next_attempt_at": leaseUntil,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// publish 投递单条消息并更新投递结果。
func (r *Relay) publish(ctx context.Context, message *Message) {
	evt, err := message.toEvent()
	if err == nil {
		err = r.bus.Publish(ctx, evt)
	}

	if err == nil {
		r.update(ctx, message.ID, map[string]any{
			"status":       string(StatusPublished),
			"published_at": time.Now().UTC(),
			"last_error":   "",
		})
		return
	}

	attempts := message.Attempts + 1
	values := map[string]any{
		"attempts":   attempts,
		"last_error": truncateError(err),
	}
	if attempts >= r.opts.MaxAttempts {
		values["status"] = string(StatusFailed)
	} else {
		values["status"] = string(StatusPending)
		values["next_attempt_at"] = time.Now().UTC().Add(r.backoff(attempts))
	}
	r.update(ctx, message.ID, values)
}

// update 写回投递结果。写回失败时消息会以租约超时的状态被重新领取。
func (r *Relay) update(ctx context.Context, id string, values map[string]any) {
	_ = r.db.WithContext(ctx).Model(&Message{}).Where("id = ?", id).Updates(values).Error
}

// backoff 计算第 attempts 次失败后的重试延迟：base * 2^(attempts-1)，并受 MaxDelay 限制。
func (r *Relay) backoff(attempts int) time.Duration {
	delay := r.opts.BaseDelay
	for i := 1; i < attempts; i++ {
		if delay >= r.opts.MaxDelay/2 {
			return r.opts.MaxDelay
		}
		delay *= 2
	}
	if delay > r.opts.MaxDelay {
		return r.opts.MaxDelay
	}
	return delay
}

// toEvent 把数据库行还原为事件。
func (m *Message) toEvent() (event.Event, error) {
	var payload any
	if len(m.Payload) > 0 {
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return event.Event{}, fmt.Errorf("outbox: decode payload: %w", err)
		}
	}

	var metadata map[string]string
	if len(m.Metadata) > 0 {
		if err := json.Unmarshal(m.Metadata, &metadata); err != nil {
			return event.Event{}, fmt.Errorf("outbox: decode metadata: %w", err)
		}
	}

	occurredAt := m.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return event.Event{
		Name:       m.Name,
		Payload:    payload,
		Metadata:   metadata,
		OccurredAt: occurredAt,
	}, nil
}

// truncateError 截断错误信息，避免超长内容写入数据库。
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > maxLastErrorLen {
		return text[:maxLastErrorLen]
	}
	return text
}

// supportsSkipLocked 判断数据库是否支持 FOR UPDATE SKIP LOCKED。
func supportsSkipLocked(db *gorm.DB) bool {
	if db == nil || db.Dialector == nil {
		return false
	}
	switch db.Dialector.Name() {
	case "mysql", "postgres":
		return true
	default:
		return false
	}
}
