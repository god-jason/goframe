package taskqueue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/god-jason/goframe/config"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// zzStartQueue 启动带 miniredis 的队列，返回 miniredis 实例供测试控制存活。
// 与 startQueue 不同：不自动 Shutdown，由测试自行收尾，便于制造 redis 已断开的场景。
func zzStartQueue(t *testing.T, concurrency int) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	dir := t.TempDir()
	path := filepath.Join(dir, "taskqueue.yaml")
	body := "taskqueue:\n  enabled: true\n  addr: " + addr + "\n  concurrency: " + strconv.Itoa(concurrency) + "\n  queues:\n    default: 1\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(func() {
		_ = Shutdown(context.Background())
		config.SetFile("")
	})
	return mr
}

// zzSwapInspector 用带钩子的 redis 客户端替换全局 inspector，用于注入命令级失败。
func zzSwapInspector(rdb redis.UniversalClient) {
	global.mu.Lock()
	global.inspector = asynq.NewInspectorFromRedisClient(rdb)
	global.mu.Unlock()
}

// zzFailHook 按命令名/参数注入失败的 redis 钩子。
type zzFailHook struct {
	shouldFail func(cmd redis.Cmder) bool
}

func (h zzFailHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h zzFailHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.shouldFail != nil && h.shouldFail(cmd) {
			return errors.New("injected redis failure")
		}
		return next(ctx, cmd)
	}
}

func (h zzFailHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func zzCmdName(name string) func(redis.Cmder) bool {
	return func(cmd redis.Cmder) bool { return cmd.Name() == name }
}

func zzIsEval(cmd redis.Cmder) bool {
	return cmd.Name() == "eval" || cmd.Name() == "evalsha"
}

func zzEvalWithArg(arg string) func(redis.Cmder) bool {
	return func(cmd redis.Cmder) bool {
		if !zzIsEval(cmd) {
			return false
		}
		for _, a := range cmd.Args() {
			if s, ok := a.(string); ok && s == arg {
				return true
			}
		}
		return false
	}
}

// TestStartupValidateError 覆盖 enabled 但缺少地址时 Startup 返回校验错误。
func TestStartupValidateError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "taskqueue.yaml")
	if err := os.WriteFile(path, []byte("taskqueue:\n  enabled: true\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	if err := config.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	t.Cleanup(func() {
		config.SetFile("")
		if err := config.Reload(); err != nil {
			t.Fatalf("reset config: %v", err)
		}
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected startup error for missing redis addr")
	}
	if Enabled() {
		t.Fatal("startup failure must not leave queue enabled")
	}
}

// TestLogBridgeAllLevels 覆盖 logBridge 的 Debug/Warn/Error/Fatal 直通方法。
func TestLogBridgeAllLevels(t *testing.T) {
	bridge := logBridge{}
	bridge.Debug("d", 1)
	bridge.Info("i", 2)
	bridge.Warn("w", 3)
	bridge.Error("e", 4)
	bridge.Fatal("f", 5)
}

// TestShutdownSharedConnectionCloseError 覆盖 Shutdown 中 inspector/client Close 失败分支。
// 通过 FromRedisClient 构造共享连接的 client/inspector，其 Close 固定返回错误。
func TestShutdownSharedConnectionCloseError(t *testing.T) {
	zzStartQueue(t, 1)
	rdb1 := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	rdb2 := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	global.mu.Lock()
	global.client = asynq.NewClientFromRedisClient(rdb1)
	global.inspector = asynq.NewInspectorFromRedisClient(rdb2)
	global.mu.Unlock()
	if err := Shutdown(context.Background()); err == nil {
		t.Fatal("expected joined close error")
	}
}

// TestInspectNotStartedErrors 覆盖 InspectQueues/InspectQueue 未启用错误。
func TestInspectNotStartedErrors(t *testing.T) {
	if _, err := InspectQueues(); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("InspectQueues error = %v", err)
	}
	if _, err := InspectQueue("default"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("InspectQueue error = %v", err)
	}
}

// TestListTasksAllStates 覆盖 pending/active/retry/completed 四种状态分支与空队列名错误。
func TestListTasksAllStates(t *testing.T) {
	zzStartQueue(t, 1)
	if err := Enqueue(context.Background(), "zz.any", []byte("p")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := ListTasks("", "pending", 1, 10); err == nil || err.Error() != "taskqueue: queue name must not be empty" {
		t.Fatalf("ListTasks empty queue error = %v", err)
	}
	for _, state := range []string{"pending", "active", "retry", "completed"} {
		if _, err := ListTasks("default", state, 1, 50); err != nil {
			t.Fatalf("list %s: %v", state, err)
		}
	}
}

// TestEnqueueAllOptions 覆盖 MaxRetry/Timeout/Unique 选项闭包与空队列名回退默认队列。
func TestEnqueueAllOptions(t *testing.T) {
	Register("zz.opts.task", func(context.Context, []byte) error { return nil })
	zzStartQueue(t, 1)
	// Unique 锁脚本在 miniredis 上可能失败，这里只要求选项闭包被执行。
	_ = Enqueue(context.Background(), "zz.opts.task", []byte("p"),
		Queue(""), MaxRetry(2), Timeout(5*time.Second), Unique(time.Minute))
}

// TestDeadRedisErrorBranches 在 redis 断开后覆盖 Queues/Inspect/List/Cancel/Enqueue 错误分支。
func TestDeadRedisErrorBranches(t *testing.T) {
	mr := zzStartQueue(t, 1)
	if err := Enqueue(context.Background(), "zz.any", []byte("p")); err != nil {
		t.Fatalf("enqueue before close: %v", err)
	}
	mr.Close()
	ctx := context.Background()
	if _, err := Queues(); err == nil {
		t.Fatal("expected queues error")
	}
	if _, err := InspectQueues(); err == nil {
		t.Fatal("expected inspect queues error")
	}
	if _, err := InspectQueue("default"); err == nil {
		t.Fatal("expected inspect queue error")
	}
	if _, err := ListTasks("default", "pending", 1, 50); err == nil {
		t.Fatal("expected list tasks error")
	}
	if err := CancelTask("default", "missing-id"); err == nil {
		t.Fatal("expected cancel task error")
	}
	if err := DeleteTask("default", "missing-id"); err == nil {
		t.Fatal("expected delete task error")
	}
	if err := RunTaskNow("default", "missing-id"); err == nil {
		t.Fatal("expected run task now error")
	}
	if err := Enqueue(ctx, "zz.any", []byte("p")); err == nil {
		t.Fatal("expected enqueue error on dead redis")
	}
}

// TestInspectQueuesGetQueueInfoError 覆盖 Queues 成功但 GetQueueInfo 失败的分支。
func TestInspectQueuesGetQueueInfoError(t *testing.T) {
	mr := zzStartQueue(t, 1)
	if err := Enqueue(context.Background(), "zz.any", []byte("p")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb.AddHook(zzFailHook{shouldFail: zzIsEval})
	zzSwapInspector(rdb)
	if _, err := InspectQueues(); err == nil {
		t.Fatal("expected get queue info error")
	}
}

// TestCancelActiveTaskPublishError 覆盖取消活跃任务时 CancelProcessing 失败分支。
func TestCancelActiveTaskPublishError(t *testing.T) {
	release := make(chan struct{})
	Register("zz.block.task", func(context.Context, []byte) error {
		<-release
		return nil
	})
	mr := zzStartQueue(t, 1)
	if err := Enqueue(context.Background(), "zz.block.task", []byte("p")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var id string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := ListTasks("default", "active", 1, 50)
		if err == nil && len(tasks) > 0 {
			id = tasks[0].ID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == "" {
		close(release)
		t.Fatal("task never became active")
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb.AddHook(zzFailHook{shouldFail: zzCmdName("publish")})
	zzSwapInspector(rdb)
	err := CancelTask("default", id)
	close(release)
	if err == nil {
		t.Fatal("expected cancel processing error")
	}
}

// TestCancelTaskArchiveError 覆盖取消非活跃任务时 ArchiveTask 失败分支。
func TestCancelTaskArchiveError(t *testing.T) {
	mr := zzStartQueue(t, 1)
	if err := Enqueue(context.Background(), "zz.any", []byte("p"), Delay(time.Hour)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var id string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := ListTasks("default", "scheduled", 1, 50)
		if err == nil && len(tasks) > 0 {
			id = tasks[0].ID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("scheduled task not found")
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb.AddHook(zzFailHook{shouldFail: zzEvalWithArg("asynq:{default}:archived")})
	zzSwapInspector(rdb)
	if err := CancelTask("default", id); err == nil {
		t.Fatal("expected archive task error")
	}
}
