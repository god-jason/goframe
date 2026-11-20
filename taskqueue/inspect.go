// 本文件提供任务队列运维接口：基于 Asynq Inspector 实现
// 队列统计、按状态列出任务以及取消/删除/立即执行操作，
// 便于管理后台或运维脚本接入。任务队列未启用或未启动时返回 ErrNotEnabled。
package taskqueue

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

// defaultPageSize 是任务列表未显式指定分页大小时返回的条数。
const defaultPageSize = 30

// QueueStat 是单个队列的统计快照。
type QueueStat struct {
	Name           string        `json:"name"`
	Size           int           `json:"size"`
	Pending        int           `json:"pending"`
	Active         int           `json:"active"`
	Scheduled      int           `json:"scheduled"`
	Retry          int           `json:"retry"`
	Archived       int           `json:"archived"`
	Completed      int           `json:"completed"`
	Aggregating    int           `json:"aggregating"`
	Processed      int           `json:"processed"`
	Failed         int           `json:"failed"`
	ProcessedTotal int           `json:"processed_total"`
	FailedTotal    int           `json:"failed_total"`
	Paused         bool          `json:"paused"`
	MemoryUsage    int64         `json:"memory_usage"`
	Latency        time.Duration `json:"latency"`
}

// TaskBrief 是任务摘要信息，用于运维列表展示。
type TaskBrief struct {
	ID            string        `json:"id"`
	Queue         string        `json:"queue"`
	Type          string        `json:"type"`
	State         string        `json:"state"`
	Payload       []byte        `json:"payload"`
	MaxRetry      int           `json:"max_retry"`
	Retried       int           `json:"retried"`
	LastErr       string        `json:"last_err"`
	LastFailedAt  time.Time     `json:"last_failed_at"`
	NextProcessAt time.Time     `json:"next_process_at"`
	CompletedAt   time.Time     `json:"completed_at"`
	Timeout       time.Duration `json:"timeout"`
}

// currentInspector 返回全局 Inspector，未启动时返回 ErrNotEnabled。
func currentInspector() (*asynq.Inspector, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	if global.inspector == nil {
		return nil, ErrNotEnabled
	}
	return global.inspector, nil
}

// Queues 返回所有队列名称。
func Queues() ([]string, error) {
	ins, err := currentInspector()
	if err != nil {
		return nil, err
	}
	names, err := ins.Queues()
	if err != nil {
		return nil, fmt.Errorf("taskqueue: list queues: %w", err)
	}
	return names, nil
}

// InspectQueues 返回所有队列的统计快照。
func InspectQueues() ([]QueueStat, error) {
	ins, err := currentInspector()
	if err != nil {
		return nil, err
	}
	names, err := ins.Queues()
	if err != nil {
		return nil, fmt.Errorf("taskqueue: list queues: %w", err)
	}
	stats := make([]QueueStat, 0, len(names))
	for _, name := range names {
		info, err := ins.GetQueueInfo(name)
		if err != nil {
			return nil, fmt.Errorf("taskqueue: inspect queue %s: %w", name, err)
		}
		stats = append(stats, toQueueStat(info))
	}
	return stats, nil
}

// InspectQueue 返回指定队列的统计快照。
func InspectQueue(name string) (*QueueStat, error) {
	ins, err := currentInspector()
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("taskqueue: queue name must not be empty")
	}
	info, err := ins.GetQueueInfo(name)
	if err != nil {
		return nil, fmt.Errorf("taskqueue: inspect queue %s: %w", name, err)
	}
	stat := toQueueStat(info)
	return &stat, nil
}

func toQueueStat(info *asynq.QueueInfo) QueueStat {
	return QueueStat{
		Name:           info.Queue,
		Size:           info.Size,
		Pending:        info.Pending,
		Active:         info.Active,
		Scheduled:      info.Scheduled,
		Retry:          info.Retry,
		Archived:       info.Archived,
		Completed:      info.Completed,
		Aggregating:    info.Aggregating,
		Processed:      info.Processed,
		Failed:         info.Failed,
		ProcessedTotal: info.ProcessedTotal,
		FailedTotal:    info.FailedTotal,
		Paused:         info.Paused,
		MemoryUsage:    info.MemoryUsage,
		Latency:        info.Latency,
	}
}

// ListTasks 按状态分页列出队列中的任务。
// state 取值：pending / active / scheduled / retry / archived / completed；
// page 从 1 开始（小于 1 视为 1），pageSize 小于等于 0 时使用默认值。
func ListTasks(queue, state string, page, pageSize int) ([]TaskBrief, error) {
	ins, err := currentInspector()
	if err != nil {
		return nil, err
	}
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return nil, errors.New("taskqueue: queue name must not be empty")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	opts := []asynq.ListOption{asynq.Page(page), asynq.PageSize(pageSize)}

	var infos []*asynq.TaskInfo
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "pending":
		infos, err = ins.ListPendingTasks(queue, opts...)
	case "active":
		infos, err = ins.ListActiveTasks(queue, opts...)
	case "scheduled":
		infos, err = ins.ListScheduledTasks(queue, opts...)
	case "retry":
		infos, err = ins.ListRetryTasks(queue, opts...)
	case "archived":
		infos, err = ins.ListArchivedTasks(queue, opts...)
	case "completed":
		infos, err = ins.ListCompletedTasks(queue, opts...)
	default:
		return nil, fmt.Errorf("taskqueue: unknown task state %q (want pending, active, scheduled, retry, archived or completed)", state)
	}
	if err != nil {
		return nil, fmt.Errorf("taskqueue: list %s tasks in %s: %w", state, queue, err)
	}
	tasks := make([]TaskBrief, 0, len(infos))
	for _, t := range infos {
		tasks = append(tasks, toTaskBrief(t))
	}
	return tasks, nil
}

func toTaskBrief(t *asynq.TaskInfo) TaskBrief {
	return TaskBrief{
		ID:            t.ID,
		Queue:         t.Queue,
		Type:          t.Type,
		State:         t.State.String(),
		Payload:       t.Payload,
		MaxRetry:      t.MaxRetry,
		Retried:       t.Retried,
		LastErr:       t.LastErr,
		LastFailedAt:  t.LastFailedAt,
		NextProcessAt: t.NextProcessAt,
		CompletedAt:   t.CompletedAt,
		Timeout:       t.Timeout,
	}
}

// CancelTask 取消任务：pending/scheduled/retry 状态的任务转入归档（死信），
// active 状态的任务发送取消信号（尽力而为）；archived/completed 状态无法取消。
func CancelTask(queue, id string) error {
	ins, err := currentInspector()
	if err != nil {
		return err
	}
	info, err := ins.GetTaskInfo(queue, id)
	if err != nil {
		return fmt.Errorf("taskqueue: get task %s: %w", id, err)
	}
	switch info.State {
	case asynq.TaskStateActive:
		if err := ins.CancelProcessing(id); err != nil {
			return fmt.Errorf("taskqueue: cancel active task %s: %w", id, err)
		}
	case asynq.TaskStatePending, asynq.TaskStateScheduled, asynq.TaskStateRetry:
		if err := ins.ArchiveTask(queue, id); err != nil {
			return fmt.Errorf("taskqueue: archive task %s: %w", id, err)
		}
	default:
		return fmt.Errorf("taskqueue: task %s in state %s cannot be canceled", id, info.State)
	}
	return nil
}

// DeleteTask 从队列中彻底删除任务。
func DeleteTask(queue, id string) error {
	ins, err := currentInspector()
	if err != nil {
		return err
	}
	if err := ins.DeleteTask(queue, id); err != nil {
		return fmt.Errorf("taskqueue: delete task %s: %w", id, err)
	}
	return nil
}

// RunTaskNow 让 scheduled/retry/archived 状态的任务立即进入待执行队列。
func RunTaskNow(queue, id string) error {
	ins, err := currentInspector()
	if err != nil {
		return err
	}
	if err := ins.RunTask(queue, id); err != nil {
		return fmt.Errorf("taskqueue: run task %s: %w", id, err)
	}
	return nil
}
