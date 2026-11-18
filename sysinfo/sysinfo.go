package sysinfo

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// Snapshot 是一次实时系统状态快照。
type Snapshot struct {
	Time   time.Time   `json:"time"`
	Host   HostInfo    `json:"host"`
	CPU    CPUUsage    `json:"cpu"`
	Memory MemoryUsage `json:"memory"`
	Disks  []DiskUsage `json:"disks"`
}

// HostInfo 是主机基础信息。
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	KernelVersion   string `json:"kernel_version"`
	KernelArch      string `json:"kernel_arch"`
	Uptime          uint64 `json:"uptime"`
	BootTime        uint64 `json:"boot_time"`
}

// CPUUsage 是 CPU 实时使用率。
type CPUUsage struct {
	Percent       float64   `json:"percent"`
	PerCore       []float64 `json:"per_core"`
	LogicalCores  int       `json:"logical_cores"`
	PhysicalCores int       `json:"physical_cores"`
	Load1         float64   `json:"load1"`
	Load5         float64   `json:"load5"`
	Load15        float64   `json:"load15"`
}

// MemoryUsage 是内存与交换分区使用情况，单位为字节。
type MemoryUsage struct {
	Total       uint64  `json:"total"`
	Available   uint64  `json:"available"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	Buffers     uint64  `json:"buffers"`
	Cached      uint64  `json:"cached"`
	UsedPercent float64 `json:"used_percent"`

	SwapTotal       uint64  `json:"swap_total"`
	SwapUsed        uint64  `json:"swap_used"`
	SwapFree        uint64  `json:"swap_free"`
	SwapUsedPercent float64 `json:"swap_used_percent"`
}

// DiskUsage 是单个分区使用情况，单位为字节。
type DiskUsage struct {
	Device      string  `json:"device"`
	Mountpoint  string  `json:"mountpoint"`
	Fstype      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`

	InodesTotal       uint64  `json:"inodes_total"`
	InodesUsed        uint64  `json:"inodes_used"`
	InodesFree        uint64  `json:"inodes_free"`
	InodesUsedPercent float64 `json:"inodes_used_percent"`
}

// 采集函数在包内测试中可替换，用于覆盖各平台的错误分支。
var (
	hostInfoWithContext       = host.InfoWithContext
	cpuPercentWithContext     = cpu.PercentWithContext
	cpuCountsWithContext      = cpu.CountsWithContext
	loadAvgWithContext        = load.AvgWithContext
	virtualMemoryWithContext  = mem.VirtualMemoryWithContext
	swapMemoryWithContext     = mem.SwapMemoryWithContext
	diskPartitionsWithContext = disk.PartitionsWithContext
	diskUsageWithContext      = disk.UsageWithContext
)

// Collect 采集当前 CPU、内存与磁盘状态。
func Collect(ctx context.Context) (*Snapshot, error) {
	return snapshot(ctx, GetConfig().normalize())
}

// CPUSnapshot 采集当前 CPU 使用率。
func CPUSnapshot(ctx context.Context) (*CPUUsage, error) {
	return cpuSnapshot(ctx, GetConfig().normalize())
}

// MemorySnapshot 采集当前内存与交换分区使用情况。
func MemorySnapshot(ctx context.Context) (*MemoryUsage, error) {
	return memorySnapshot(ctx)
}

// DiskSnapshot 采集当前分区使用情况。
func DiskSnapshot(ctx context.Context) ([]DiskUsage, error) {
	return diskSnapshot(ctx)
}

func snapshot(ctx context.Context, cfg Config) (*Snapshot, error) {
	hostInfo := hostSnapshot(ctx)

	usage, err := cpuSnapshot(ctx, cfg)
	if err != nil {
		return nil, err
	}
	memory, err := memorySnapshot(ctx)
	if err != nil {
		return nil, err
	}
	disks, err := diskSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return &Snapshot{
		Time:   time.Now(),
		Host:   hostInfo,
		CPU:    *usage,
		Memory: *memory,
		Disks:  disks,
	}, nil
}

func hostSnapshot(ctx context.Context) HostInfo {
	info, err := hostInfoWithContext(ctx)
	if err != nil {
		return HostInfo{}
	}
	return HostInfo{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		KernelArch:      info.KernelArch,
		Uptime:          info.Uptime,
		BootTime:        info.BootTime,
	}
}

func cpuSnapshot(ctx context.Context, cfg Config) (*CPUUsage, error) {
	percents, err := cpuPercentWithContext(ctx, cfg.SampleInterval, true)
	if err != nil {
		return nil, fmt.Errorf("sysinfo: cpu usage: %w", err)
	}

	var total float64
	for _, percent := range percents {
		total += percent
	}
	if len(percents) > 0 {
		total /= float64(len(percents))
	}

	logical, _ := cpuCountsWithContext(ctx, true)
	physical, _ := cpuCountsWithContext(ctx, false)

	usage := &CPUUsage{
		Percent:       total,
		PerCore:       percents,
		LogicalCores:  logical,
		PhysicalCores: physical,
	}
	// 负载均值在部分平台不可用，失败时保持为 0。
	if average, err := loadAvgWithContext(ctx); err == nil {
		usage.Load1 = average.Load1
		usage.Load5 = average.Load5
		usage.Load15 = average.Load15
	}
	return usage, nil
}

func memorySnapshot(ctx context.Context) (*MemoryUsage, error) {
	virtual, err := virtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("sysinfo: memory usage: %w", err)
	}
	usage := &MemoryUsage{
		Total:       virtual.Total,
		Available:   virtual.Available,
		Used:        virtual.Used,
		Free:        virtual.Free,
		Buffers:     virtual.Buffers,
		Cached:      virtual.Cached,
		UsedPercent: virtual.UsedPercent,
	}
	// 部分平台没有交换分区，失败时保持为 0。
	if swap, err := swapMemoryWithContext(ctx); err == nil {
		usage.SwapTotal = swap.Total
		usage.SwapUsed = swap.Used
		usage.SwapFree = swap.Free
		usage.SwapUsedPercent = swap.UsedPercent
	}
	return usage, nil
}

func diskSnapshot(ctx context.Context) ([]DiskUsage, error) {
	partitions, err := diskPartitionsWithContext(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("sysinfo: disk partitions: %w", err)
	}

	disks := make([]DiskUsage, 0, len(partitions))
	for _, partition := range partitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if partition.Mountpoint == "" {
			continue
		}
		usage, err := diskUsageWithContext(ctx, partition.Mountpoint)
		if err != nil {
			// 单个分区不可访问时跳过，不影响其他分区。
			continue
		}
		disks = append(disks, DiskUsage{
			Device:            partition.Device,
			Mountpoint:        partition.Mountpoint,
			Fstype:            partition.Fstype,
			Total:             usage.Total,
			Used:              usage.Used,
			Free:              usage.Free,
			UsedPercent:       usage.UsedPercent,
			InodesTotal:       usage.InodesTotal,
			InodesUsed:        usage.InodesUsed,
			InodesFree:        usage.InodesFree,
			InodesUsedPercent: usage.InodesUsedPercent,
		})
	}
	return disks, nil
}
