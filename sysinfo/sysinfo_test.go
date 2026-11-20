package sysinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/god-jason/goframe/api"
)

type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Path = "/system"
	cfg.SampleInterval = 20 * time.Millisecond
	cfg.Timeout = 5 * time.Second
	return cfg
}

// mountTestEngine 把当前注册表中的框架根路径接口挂载到新引擎。
// 单元测试直接验证处理器，跳过管理员守卫。
func mountTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := api.MountRoot(engine, api.WithGuard(api.Admin)); err != nil {
		t.Fatalf("mount root: %v", err)
	}
	return engine
}

// TestSystemInfoRequiresAdmin 验证系统信息接口默认拒绝匿名访问。
func TestSystemInfoRequiresAdmin(t *testing.T) {
	api.Reset()
	defer api.Reset()
	register(testConfig())

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := api.MountRoot(engine); err != nil {
		t.Fatalf("mount root: %v", err)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/system", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestRegisterDisabled(t *testing.T) {
	api.Reset()
	register(DefaultConfig())
	engine := mountTestEngine(t)
	if len(engine.Routes()) != 0 {
		t.Fatalf("routes = %d, want 0", len(engine.Routes()))
	}
}

func TestHandlers(t *testing.T) {
	api.Reset()
	register(testConfig())
	engine := mountTestEngine(t)

	var snapshot envelope[Snapshot]
	getJSON(t, engine, "/system", &snapshot)
	if snapshot.Code != 0 {
		t.Fatalf("snapshot code = %d, message = %q", snapshot.Code, snapshot.Message)
	}
	if snapshot.Data.CPU.Percent < 0 || snapshot.Data.CPU.Percent > 100 {
		t.Fatalf("cpu percent = %v", snapshot.Data.CPU.Percent)
	}
	if snapshot.Data.Memory.Total == 0 {
		t.Fatal("memory total is zero")
	}
	if len(snapshot.Data.CPU.PerCore) == 0 && snapshot.Data.CPU.LogicalCores > 0 {
		t.Fatal("per-core usage is empty")
	}

	var cpu envelope[CPUUsage]
	getJSON(t, engine, "/system/cpu", &cpu)
	if cpu.Code != 0 || cpu.Data.LogicalCores == 0 {
		t.Fatalf("cpu = %+v", cpu)
	}

	var memory envelope[MemoryUsage]
	getJSON(t, engine, "/system/memory", &memory)
	if memory.Code != 0 || memory.Data.Total == 0 {
		t.Fatalf("memory = %+v", memory)
	}

	var disks envelope[[]DiskUsage]
	getJSON(t, engine, "/system/disk", &disks)
	if disks.Code != 0 {
		t.Fatalf("disks = %+v", disks)
	}
}

func TestNormalize(t *testing.T) {
	cfg := Config{}.normalize()
	defaults := DefaultConfig()
	if cfg.Path != defaults.Path || cfg.SampleInterval != defaults.SampleInterval || cfg.Timeout != defaults.Timeout {
		t.Fatalf("normalize = %+v", cfg)
	}

	cfg = Config{SampleInterval: 2 * time.Second, Timeout: time.Second}.normalize()
	if cfg.Timeout <= cfg.SampleInterval {
		t.Fatalf("timeout must exceed sample interval: %+v", cfg)
	}
}

func getJSON(t *testing.T, engine *gin.Engine, path string, target any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body = %s", path, recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("%s decode: %v", path, err)
	}
}

// useFakeCollectors 用可控实现替换采集函数，测试结束后恢复。
func useFakeCollectors(t *testing.T) {
	t.Helper()
	oldHost := hostInfoWithContext
	oldPercent := cpuPercentWithContext
	oldCounts := cpuCountsWithContext
	oldLoad := loadAvgWithContext
	oldVirtual := virtualMemoryWithContext
	oldSwap := swapMemoryWithContext
	oldPartitions := diskPartitionsWithContext
	oldUsage := diskUsageWithContext
	t.Cleanup(func() {
		hostInfoWithContext = oldHost
		cpuPercentWithContext = oldPercent
		cpuCountsWithContext = oldCounts
		loadAvgWithContext = oldLoad
		virtualMemoryWithContext = oldVirtual
		swapMemoryWithContext = oldSwap
		diskPartitionsWithContext = oldPartitions
		diskUsageWithContext = oldUsage
	})

	hostInfoWithContext = func(context.Context) (*host.InfoStat, error) {
		return &host.InfoStat{Hostname: "host", OS: "test", Uptime: 1}, nil
	}
	cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
		return []float64{10, 20}, nil
	}
	cpuCountsWithContext = func(context.Context, bool) (int, error) { return 4, nil }
	loadAvgWithContext = func(context.Context) (*load.AvgStat, error) {
		return &load.AvgStat{Load1: 1, Load5: 2, Load15: 3}, nil
	}
	virtualMemoryWithContext = func(context.Context) (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{Total: 100, Available: 50, Used: 50, Free: 40, Buffers: 1, Cached: 2, UsedPercent: 50}, nil
	}
	swapMemoryWithContext = func(context.Context) (*mem.SwapMemoryStat, error) {
		return &mem.SwapMemoryStat{Total: 10, Used: 1, Free: 9, UsedPercent: 10}, nil
	}
	diskPartitionsWithContext = func(context.Context, bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{{Device: "d", Mountpoint: "/", Fstype: "ext"}}, nil
	}
	diskUsageWithContext = func(context.Context, string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Total: 100, Used: 40, Free: 60, UsedPercent: 40}, nil
	}
}

func TestSnapshotWithFakeCollectors(t *testing.T) {
	useFakeCollectors(t)

	snapshotValue, err := snapshot(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshotValue.Host.Hostname != "host" || snapshotValue.CPU.LogicalCores != 4 {
		t.Fatalf("unexpected snapshot: %+v", snapshotValue)
	}
	if snapshotValue.CPU.Load1 != 1 || snapshotValue.Memory.SwapTotal != 10 {
		t.Fatalf("optional metrics not filled: %+v", snapshotValue)
	}
	if len(snapshotValue.Disks) != 1 || snapshotValue.Disks[0].Total != 100 {
		t.Fatalf("unexpected disks: %+v", snapshotValue.Disks)
	}
}

func TestOptionalMetricsErrorsKeepZero(t *testing.T) {
	useFakeCollectors(t)
	loadAvgWithContext = func(context.Context) (*load.AvgStat, error) {
		return nil, errors.New("no load")
	}
	swapMemoryWithContext = func(context.Context) (*mem.SwapMemoryStat, error) {
		return nil, errors.New("no swap")
	}

	usage, err := cpuSnapshot(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("cpu: %v", err)
	}
	if usage.Load1 != 0 {
		t.Fatalf("load should be zero: %+v", usage)
	}
	memory, err := memorySnapshot(context.Background())
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	if memory.SwapTotal != 0 {
		t.Fatalf("swap should be zero: %+v", memory)
	}
}

func TestHostSnapshotError(t *testing.T) {
	useFakeCollectors(t)
	hostInfoWithContext = func(context.Context) (*host.InfoStat, error) {
		return nil, errors.New("no host")
	}

	snapshotValue, err := snapshot(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshotValue.Host.Hostname != "" {
		t.Fatalf("host should be empty: %+v", snapshotValue.Host)
	}
}

func TestSnapshotErrors(t *testing.T) {
	cases := []struct {
		name      string
		configure func()
	}{
		{"cpu", func() {
			cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
				return nil, errors.New("cpu down")
			}
		}},
		{"memory", func() {
			virtualMemoryWithContext = func(context.Context) (*mem.VirtualMemoryStat, error) {
				return nil, errors.New("memory down")
			}
		}},
		{"disk", func() {
			diskPartitionsWithContext = func(context.Context, bool) ([]disk.PartitionStat, error) {
				return nil, errors.New("disk down")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFakeCollectors(t)
			tc.configure()
			if _, err := snapshot(context.Background(), testConfig()); err == nil {
				t.Fatal("expected snapshot error")
			}
		})
	}
}

func TestDiskSnapshotSkipsInvalidPartitions(t *testing.T) {
	useFakeCollectors(t)
	diskPartitionsWithContext = func(context.Context, bool) ([]disk.PartitionStat, error) {
		return []disk.PartitionStat{
			{Mountpoint: ""},
			{Mountpoint: "/bad"},
			{Mountpoint: "/ok"},
		}, nil
	}
	diskUsageWithContext = func(_ context.Context, mountpoint string) (*disk.UsageStat, error) {
		if mountpoint == "/bad" {
			return nil, errors.New("inaccessible")
		}
		return &disk.UsageStat{Total: 1}, nil
	}

	disks, err := diskSnapshot(context.Background())
	if err != nil {
		t.Fatalf("disk: %v", err)
	}
	if len(disks) != 1 || disks[0].Mountpoint != "/ok" {
		t.Fatalf("unexpected disks: %+v", disks)
	}
}

func TestDiskSnapshotCanceledContext(t *testing.T) {
	useFakeCollectors(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := diskSnapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestPublicCollectors(t *testing.T) {
	if GetConfig() == nil {
		t.Fatal("current config is nil")
	}
	if _, err := Collect(context.Background()); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, err := CPUSnapshot(context.Background()); err != nil {
		t.Fatalf("cpu snapshot: %v", err)
	}
	if _, err := MemorySnapshot(context.Background()); err != nil {
		t.Fatalf("memory snapshot: %v", err)
	}
	if _, err := DiskSnapshot(context.Background()); err != nil {
		t.Fatalf("disk snapshot: %v", err)
	}
}

func TestRegisterPublic(t *testing.T) {
	api.Reset()
	Register()
	engine := mountTestEngine(t)
	if len(engine.Routes()) != 0 {
		t.Fatalf("routes = %d, want 0 when disabled", len(engine.Routes()))
	}
}

func TestHandlerErrors(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		configure func()
	}{
		{"snapshot", "/system", func() {
			cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
				return nil, errors.New("cpu down")
			}
		}},
		{"cpu", "/system/cpu", func() {
			cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
				return nil, errors.New("cpu down")
			}
		}},
		{"memory", "/system/memory", func() {
			virtualMemoryWithContext = func(context.Context) (*mem.VirtualMemoryStat, error) {
				return nil, errors.New("memory down")
			}
		}},
		{"disk", "/system/disk", func() {
			diskPartitionsWithContext = func(context.Context, bool) ([]disk.PartitionStat, error) {
				return nil, errors.New("disk down")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFakeCollectors(t)
			tc.configure()
			api.Reset()
			register(testConfig())
			engine := mountTestEngine(t)

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", recorder.Code)
			}
		})
	}
}

func TestHandlerTimeout(t *testing.T) {
	useFakeCollectors(t)
	cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
		return nil, context.DeadlineExceeded
	}
	api.Reset()
	register(testConfig())
	engine := mountTestEngine(t)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/system/cpu", nil))
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", recorder.Code)
	}
}

func TestHandlerTimeoutFromRequestContext(t *testing.T) {
	useFakeCollectors(t)
	cpuPercentWithContext = func(context.Context, time.Duration, bool) ([]float64, error) {
		return nil, errors.New("generic")
	}
	api.Reset()
	register(testConfig())
	engine := mountTestEngine(t)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/system/cpu", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", recorder.Code)
	}
}

func TestResolvePath(t *testing.T) {
	cases := map[string]string{
		"":             "/system",
		"   ":          "/system",
		"system":       "/system",
		"/system/":     "/system",
		" /system/// ": "/system",
		"/":            "/system",
		"/metrics":     "/metrics",
	}
	for input, want := range cases {
		if got := resolvePath(input); got != want {
			t.Fatalf("resolvePath(%q) = %q, want %q", input, got, want)
		}
	}
}
