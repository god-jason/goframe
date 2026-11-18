package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/god-jason/goframe/event"
	"github.com/god-jason/goframe/id"
)

// Record 在调用方事务内写入一条待投递事件，必须与业务数据写入共用 tx，
// 以保证业务数据与事件同时提交或回滚。
func Record(ctx context.Context, tx *gorm.DB, ev event.Event) error {
	if tx == nil {
		return fmt.Errorf("outbox: transaction is nil")
	}
	if ev.Name == "" {
		return fmt.Errorf("outbox: event name must not be empty")
	}

	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("outbox: encode payload: %w", err)
	}
	var metadata []byte
	if len(ev.Metadata) > 0 {
		metadata, err = json.Marshal(ev.Metadata)
		if err != nil {
			return fmt.Errorf("outbox: encode metadata: %w", err)
		}
	}

	now := time.Now().UTC()
	msg := Message{
		ID:            id.New(),
		Name:          ev.Name,
		Payload:       payload,
		Metadata:      metadata,
		Status:        string(StatusPending),
		Attempts:      0,
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	if err := tx.WithContext(ctx).Create(&msg).Error; err != nil {
		return fmt.Errorf("outbox: record event %q: %w", ev.Name, err)
	}
	return nil
}
