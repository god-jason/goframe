// Package scheduler 定义定时任务接口与 cron 实现。
package scheduler

import (
	"context"
	"time"
)

// Scheduler 是框架统一定时任务接口。
type Scheduler interface {
	// Register 注册定时任务，spec 支持标准 5 段 cron 表达式与可选秒字段。
	Register(name string, spec string, fn func(ctx context.Context) error) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// Jobs 返回已注册任务快照。
	Jobs() []Job
}

// Job 描述一个已注册任务。
type Job struct {
	Name    string    `json:"name"`
	Spec    string    `json:"spec"`
	Next    time.Time `json:"next"`
	Running bool      `json:"running"`
}
