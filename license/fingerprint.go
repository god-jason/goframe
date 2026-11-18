package license

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
)

// 指纹来源。默认按 uuid、hostid、disk、cpuid、mac 的顺序取第一个可用值。
const (
	SourceUUID   = "uuid"
	SourceHostID = "hostid"
	SourceDisk   = "disk"
	SourceCPUID  = "cpuid"
	SourceMAC    = "mac"
)

// DefaultFingerprintSources 是默认指纹来源优先级。
var DefaultFingerprintSources = []string{SourceUUID, SourceHostID, SourceDisk, SourceCPUID, SourceMAC}

// 平台与文件读取间接层，便于测试覆盖各平台分支。
var (
	currentGOOS = runtime.GOOS
	readFileFn  = os.ReadFile
)

// FingerprintOptions 是机器指纹采集选项。
type FingerprintOptions struct {
	// Sources 指定来源优先级；为空时使用 DefaultFingerprintSources。
	Sources []string
}

// FingerprintInfo 是机器指纹采集结果。
type FingerprintInfo struct {
	// Fingerprint 是选中来源的 sha256 指纹。
	Fingerprint string `json:"fingerprint"`
	// Source 是实际采用的来源。
	Source string `json:"source"`
	// Components 是各来源的原始值，仅用于诊断，不应写入日志或对外暴露。
	Components map[string]string `json:"components,omitempty"`
}

// CollectFingerprint 按优先级采集机器指纹。
//
// 优先使用硬件 UUID、系统 MachineID、磁盘序列号等稳定来源，
// MAC 仅作为最后兜底，并过滤虚拟网卡与本机管理地址，避免 MAC 漂移导致指纹变化。
func CollectFingerprint(ctx context.Context, options FingerprintOptions) (FingerprintInfo, error) {
	components := map[string]string{
		SourceUUID:   physicalUUID(ctx),
		SourceHostID: systemHostID(ctx),
		SourceDisk:   diskSerial(ctx),
		SourceCPUID:  cpuSignature(ctx),
		SourceMAC:    stableMAC(),
	}

	sources := options.Sources
	if len(sources) == 0 {
		sources = DefaultFingerprintSources
	}
	for _, source := range sources {
		source = strings.ToLower(strings.TrimSpace(source))
		value := strings.TrimSpace(components[source])
		if value == "" {
			continue
		}
		sum := sha256.Sum256([]byte(source + "|" + value))
		return FingerprintInfo{
			Fingerprint: hex.EncodeToString(sum[:]),
			Source:      source,
			Components:  components,
		}, nil
	}
	return FingerprintInfo{Components: components}, fmt.Errorf("license: cannot determine machine fingerprint")
}

// Fingerprint 使用默认来源计算机器指纹。
func Fingerprint(ctx context.Context) (string, error) {
	info, err := CollectFingerprint(ctx, FingerprintOptions{})
	if err != nil {
		return "", err
	}
	return info.Fingerprint, nil
}

// systemHostID 返回操作系统 MachineID/MachineGuid。
func systemHostID(ctx context.Context) string {
	value, err := host.HostIDWithContext(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// cpuSignature 返回 CPU 厂商与型号签名。该值稳定但不保证全局唯一，只作为兜底来源。
func cpuSignature(ctx context.Context) string {
	infos, err := cpu.InfoWithContext(ctx)
	if err != nil || len(infos) == 0 {
		return ""
	}
	first := infos[0]
	return fmt.Sprintf("%s|%s|%s|%d|%s",
		first.VendorID, first.Family, first.Model, first.Stepping, strings.TrimSpace(first.ModelName))
}

// physicalUUID 返回硬件 UUID。Linux 读取 DMI，macOS/Windows 通过系统命令查询。
func physicalUUID(ctx context.Context) string {
	switch currentGOOS {
	case "linux":
		for _, path := range []string{
			"/sys/class/dmi/id/product_uuid",
			"/sys/class/dmi/id/board_serial",
		} {
			data, err := readFileFn(path)
			if err != nil {
				continue
			}
			if value := normalizeUUID(string(data)); value != "" {
				return value
			}
		}
	case "darwin":
		output := commandOutput(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
		if value := parseIOPlatformUUID(output); value != "" {
			return value
		}
	case "windows":
		output := commandOutput(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
			"(Get-CimInstance Win32_ComputerSystemProduct).UUID")
		if value := normalizeUUID(output); value != "" {
			return value
		}
		output = commandOutput(ctx, "wmic", "csproduct", "get", "uuid")
		if value := normalizeUUID(output); value != "" {
			return value
		}
	}
	return ""
}

// stableMAC 返回稳定网卡 MAC。优先全局管理地址，过滤常见虚拟网卡。
func stableMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	primary := make([]string, 0, len(interfaces))
	fallback := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 || virtualInterface(iface.Name) {
			continue
		}
		mac := iface.HardwareAddr.String()
		fallback = append(fallback, mac)
		// 首字节的 0x02 位表示本地管理地址，通常来自虚拟网卡或随机 MAC。
		if iface.HardwareAddr[0]&0x02 == 0 {
			primary = append(primary, mac)
		}
	}

	selected := primary
	if len(selected) == 0 {
		selected = fallback
	}
	if len(selected) == 0 {
		return ""
	}
	sort.Strings(selected)
	return strings.Join(selected, ",")
}

// virtualInterface 判断是否为常见虚拟/隧道网卡。
func virtualInterface(name string) bool {
	name = strings.ToLower(name)
	prefixes := []string{
		"docker", "veth", "virbr", "br-", "vmnet", "vbox", "hyper-v", "vethernet",
		"tap", "tun", "utun", "tailscale", "zerotier", "wg", "wireguard",
		"bluetooth", "bt-", "pan", "awdl", "llw", "rmnet", "wwan", "dummy",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// commandOutput 执行系统命令并返回标准输出，超时或失败时返回空字符串。
func commandOutput(ctx context.Context, name string, args ...string) string {
	if ctx == nil {
		ctx = context.Background()
	}
	commandCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(commandCtx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(output)
}

// parseIOPlatformUUID 从 ioreg 输出中解析 IOPlatformUUID。
func parseIOPlatformUUID(output string) string {
	const marker = "IOPlatformUUID\" = \""
	index := strings.Index(output, marker)
	if index < 0 {
		return ""
	}
	value := output[index+len(marker):]
	if end := strings.Index(value, "\""); end >= 0 {
		value = value[:end]
	}
	return normalizeUUID(value)
}

// normalizeUUID 清理并校验 UUID，过滤全零与常见虚拟机占位值。
func normalizeUUID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"")
	if strings.Contains(value, "\n") {
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 36 && strings.Count(line, "-") == 4 {
				value = line
				break
			}
		}
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 36 || strings.Count(value, "-") != 4 {
		return ""
	}
	switch value {
	case "00000000-0000-0000-0000-000000000000",
		"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF",
		"03000200-0400-0500-0006-000700080009":
		return ""
	}
	return value
}
