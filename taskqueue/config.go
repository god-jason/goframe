package taskqueue

import (
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
)

// Config 是任务队列配置。
type Config struct {
	// Enabled 控制是否启用任务队列。
	Enabled bool `mapstructure:"enabled"`
	// Addr 是 Redis 地址；为空时复用 redis 段的连接配置。
	Addr string `mapstructure:"addr"`
	// Username 是 Redis ACL 用户名（Addr 显式配置时生效）。
	Username string `mapstructure:"username"`
	// Password 是 Redis 密码（Addr 显式配置时生效）。
	Password string `mapstructure:"password"`
	// DB 是 Redis 库编号。
	DB int `mapstructure:"db"`
	// Concurrency 是 worker 并发量。
	Concurrency int `mapstructure:"concurrency"`
	// Queues 是队列名到权重的映射，决定各队列的消费比例。
	Queues map[string]int `mapstructure:"queues"`
	// StrictPriority 为 true 时严格按队列优先级消费，而非按权重轮转。
	StrictPriority bool `mapstructure:"strict_priority"`
	// ShutdownTimeout 是优雅停机等待任务完成的最长时间。
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	// MaxRetry 是入队任务的默认重试次数。
	MaxRetry int `mapstructure:"max_retry"`
	// TaskTimeout 是单个任务的默认执行超时。
	TaskTimeout time.Duration `mapstructure:"task_timeout"`
	// LogLevel 是 Asynq 内部日志级别：debug / info / warn / error / fatal。
	LogLevel string `mapstructure:"log_level"`
}

// DefaultConfig 返回任务队列默认配置。
func DefaultConfig() Config {
	return Config{
		Concurrency:     10,
		Queues:          map[string]int{"default": 10, "notify": 5},
		ShutdownTimeout: 8 * time.Second,
		MaxRetry:        3,
		TaskTimeout:     time.Minute,
		LogLevel:        "info",
	}
}

var cfg = config.Register("taskqueue", DefaultConfig())

// GetConfig 返回当前任务队列配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置，保证组件可安全启动。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if c.Concurrency <= 0 {
		c.Concurrency = defaults.Concurrency
	}
	if len(c.Queues) == 0 {
		c.Queues = defaults.Queues
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaults.ShutdownTimeout
	}
	if c.MaxRetry < 0 {
		c.MaxRetry = defaults.MaxRetry
	}
	if c.TaskTimeout < 0 {
		c.TaskTimeout = defaults.TaskTimeout
	}
	if strings.TrimSpace(c.LogLevel) == "" {
		c.LogLevel = defaults.LogLevel
	}
	return c
}

// Validate 校验启用状态下的必填配置。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.redisOptions().Addr) == "" {
		return fmt.Errorf("taskqueue: redis addr is required when enabled (set taskqueue.addr or the redis section)")
	}
	return nil
}

// redisOptions 构建 Asynq Redis 连接参数：
// 显式配置 taskqueue.addr 时使用本段凭据，否则整体复用 redis 段配置。
func (c Config) redisOptions() asynq.RedisClientOpt {
	if addr := strings.TrimSpace(c.Addr); addr != "" {
		return asynq.RedisClientOpt{Addr: addr, Username: c.Username, Password: c.Password, DB: c.DB}
	}
	redisCfg := cache.GetConfig()
	db := c.DB
	if db == 0 {
		db = redisCfg.DB
	}
	return asynq.RedisClientOpt{Addr: redisCfg.Addr, Username: redisCfg.Username, Password: redisCfg.Password, DB: db}
}
