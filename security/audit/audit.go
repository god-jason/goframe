// Package audit 提供安全审计能力。
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/god-jason/goframe/logger"
)

// Event 是审计事件。审计事件不可由普通用户修改或删除。
type Event struct {
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id"`
	UserID       string         `json:"user_id,omitempty"`
	TenantID     string         `json:"tenant_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Result       string         `json:"result"`
	ReasonCode   string         `json:"reason_code,omitempty"`
	SourceIP     string         `json:"source_ip,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	TraceID      string         `json:"trace_id,omitempty"`
	Detail       map[string]any `json:"detail,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// Auditor 是框架统一审计接口。
type Auditor interface {
	Record(ctx context.Context, event Event) error
}

// LogAuditor 将审计事件写入日志，并通过限制 detail 大小防止日志膨胀。
type LogAuditor struct {
	log       logger.Logger
	maxDetail int
}

// NewLogAuditor 创建日志审计器，maxDetail 为 detail JSON 的最大字节数。
func NewLogAuditor(log logger.Logger, maxDetail int64) *LogAuditor {
	max := int(maxDetail)
	if max <= 0 {
		max = 4 << 10
	}
	return &LogAuditor{log: log, maxDetail: max}
}

// Record 记录审计事件。
func (a *LogAuditor) Record(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	fields := []logger.Field{
		logger.String("action", event.Action),
		logger.String("actor_type", event.ActorType),
		logger.String("actor_id", event.ActorID),
		logger.String("user_id", event.UserID),
		logger.String("tenant_id", event.TenantID),
		logger.String("resource_type", event.ResourceType),
		logger.String("resource_id", event.ResourceID),
		logger.String("result", event.Result),
		logger.String("reason_code", event.ReasonCode),
		logger.String("source_ip", event.SourceIP),
		logger.String("user_agent", event.UserAgent),
		logger.String("request_id", event.RequestID),
		logger.String("trace_id", event.TraceID),
		logger.Time("created_at", event.CreatedAt),
	}
	if len(event.Detail) > 0 {
		if data, err := json.Marshal(event.Detail); err == nil {
			if len(data) > a.maxDetail {
				data = data[:a.maxDetail]
			}
			fields = append(fields, logger.String("detail", string(data)))
		}
	}
	a.log.Info("audit event", fields...)
	return nil
}

// NopAuditor 丢弃所有审计事件，仅用于未启用审计的场景。
type NopAuditor struct{}

// Record 实现 Auditor。
func (NopAuditor) Record(context.Context, Event) error { return nil }
