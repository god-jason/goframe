// Package outbox 提供基于数据库事务的可靠事件投递（Transactional Outbox）。
//
// 业务在写库事务中调用 Record 写入事件，事务提交后由 Relay 后台批量领取并
// 投递到 EventBus。即使投递失败也会按指数退避重试，超过最大次数后标记 failed，
// 从而保证 at-least-once 语义。
package outbox

import "time"

// Status 是 outbox 消息状态。
type Status string

const (
	// StatusPending 等待投递。
	StatusPending Status = "pending"
	// StatusPublishing 已被某个 Relay 领取，正在投递（同时是租约标记）。
	StatusPublishing Status = "publishing"
	// StatusPublished 已成功投递。
	StatusPublished Status = "published"
	// StatusFailed 超过最大重试次数，不再自动投递。
	StatusFailed Status = "failed"
)

// Message 是 outbox 表模型。
type Message struct {
	// ID 为 UUIDv7 字符串，按时间有序。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// Name 是事件名。
	Name string `gorm:"column:name;type:varchar(255);not null;index:idx_event_outbox_name"`
	// Payload 是事件负载的 JSON 编码。
	Payload []byte `gorm:"column:payload;type:json;not null"`
	// Metadata 是事件元数据的 JSON 编码，可为空。
	Metadata []byte `gorm:"column:metadata;type:json"`
	// Status 是当前投递状态。
	Status string `gorm:"column:status;type:varchar(16);not null;index:idx_event_outbox_dispatch,priority:1"`
	// Attempts 是已失败的投递次数。
	Attempts int `gorm:"column:attempts;not null;default:0"`
	// NextAttemptAt 是下一次可投递时间；领取中的消息用它表示租约到期时间。
	NextAttemptAt time.Time `gorm:"column:next_attempt_at;not null;index:idx_event_outbox_dispatch,priority:2"`
	// CreatedAt 是消息写入时间。
	CreatedAt time.Time `gorm:"column:created_at;not null;index:idx_event_outbox_created"`
	// PublishedAt 是成功投递时间。
	PublishedAt *time.Time `gorm:"column:published_at"`
	// LastError 是最近一次投递失败原因。
	LastError string `gorm:"column:last_error;type:text"`
}

// TableName 返回 outbox 表名。
func (Message) TableName() string { return "event_outbox" }
