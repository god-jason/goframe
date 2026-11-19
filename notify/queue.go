package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/god-jason/goframe/taskqueue"
)

// TaskType 是通知消息在任务队列中的任务类型。
const TaskType = "notify:send"

// Send 派发一条消息：任务队列已启用时入队异步发送（自动重试），
// 否则回退为使用当前全局发送器同步发送。
func Send(ctx context.Context, msg Message) error {
	if taskqueue.Enabled() {
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("notify: encode message: %w", err)
		}
		current := GetConfig().normalize()
		return taskqueue.Enqueue(ctx, TaskType, payload,
			taskqueue.Queue(current.Queue),
			taskqueue.MaxRetry(current.MaxRetry),
		)
	}
	sender := GetSender()
	if sender == nil {
		return fmt.Errorf("notify: no sender configured")
	}
	return sender.Send(ctx, msg)
}

// handleQueueTask 消费队列中的通知任务。
func handleQueueTask(ctx context.Context, payload []byte) error {
	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		return fmt.Errorf("notify: decode message: %w", err)
	}
	sender := GetSender()
	if sender == nil {
		return fmt.Errorf("notify: sender not configured")
	}
	return sender.Send(ctx, msg)
}
