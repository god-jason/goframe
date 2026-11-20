package license

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 说明：以下残留块经分析在本环境（Windows，非管理员）逻辑不可达，保留源码并在此注释：
//   - crypto.go:44/48/52（ed25519 生成/序列化失败）：crypto/rand 与 x509 序列化 Ed25519 不会失败。
//   - crypto.go:108/118（License/envelope 的 JSON 编码失败）：License 与 envelope 均为纯值类型，
//     不含 channel/func/NaN，json.Marshal 不会失败。
//   - revocation.go:47/57（吊销列表 JSON 编码失败）：同上。
//   - fingerprint.go:155/165/177/180（stableMAC 各分支）：依赖宿主机网卡状态，
//     无法在测试中注入；本机无虚拟/本地管理 MAC，无法触发 primary 为空或 selected 为空。
//   - manager.go:260（filepath.Dir 返回空）：filepath.Dir 始终返回非空（至少 "."）。
//   - manager.go:267（stateData JSON 编码失败）：stateData 为纯值类型，不会失败。
//   - manager.go:279/283/287（临时文件写入/Chmod/Close 失败）：本地文件系统无法确定性触发。

// TestFingerprintErrorAllSourcesEmpty 覆盖 Fingerprint 在所有来源均不可用时返回错误的路径。
func TestFingerprintErrorAllSourcesEmpty(t *testing.T) {
	orig := DefaultFingerprintSources
	DefaultFingerprintSources = []string{"bogus-source"}
	t.Cleanup(func() { DefaultFingerprintSources = orig })

	if _, err := Fingerprint(context.Background()); err == nil {
		t.Fatal("expected error when all sources are bogus")
	}
}

// 说明：fingerprint.go:100（host.HostIDWithContext 失败）与 109（cpu.InfoWithContext 失败）
// 在本环境不可达：gopsutil v4 的 Windows 实现直接读注册表/系统信息，忽略上下文取消，
// 且本机采集恒成功，无法确定性构造错误分支。

// TestPhysicalUUIDDarwinIOReg 通过 PATH 注入 ioreg.bat 覆盖 darwin 分支的 ioreg 解析路径。
func TestPhysicalUUIDDarwinIOReg(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "ioreg.bat")
	// 输出包含 IOPlatformUUID 标记，触发 parseIOPlatformUUID 成功。
	content := "@echo off\r\necho \"IOPlatformUUID\" = \"4C4C4544-0030-3010-8043-B4C04F4E4D32\"\r\n"
	if err := os.WriteFile(bat, []byte(content), 0o644); err != nil {
		t.Fatalf("write bat: %v", err)
	}

	origGOOS := currentGOOS
	origPATH := os.Getenv("PATH")
	currentGOOS = "darwin"
	os.Setenv("PATH", dir+string(os.PathListSeparator)+origPATH)
	t.Cleanup(func() {
		currentGOOS = origGOOS
		os.Setenv("PATH", origPATH)
	})

	got := physicalUUID(context.Background())
	if got != "4C4C4544-0030-3010-8043-B4C04F4E4D32" {
		t.Fatalf("physicalUUID = %q", got)
	}
}

// TestPhysicalUUIDWindowsWMIC 通过 PATH 注入 wmic.bat 覆盖 windows 分支的 wmic 兜底路径。
// 同时注入一个失败的 powershell.bat，强制走 wmic 分支。
func TestPhysicalUUIDWindowsWMIC(t *testing.T) {
	dir := t.TempDir()
	ps := filepath.Join(dir, "powershell.bat")
	if err := os.WriteFile(ps, []byte("@echo off\r\nexit /b 1\r\n"), 0o644); err != nil {
		t.Fatalf("write ps bat: %v", err)
	}
	wmic := filepath.Join(dir, "wmic.bat")
	wmicContent := "@echo off\r\necho 4C4C4544-0030-3010-8043-B4C04F4E4D32\r\n"
	if err := os.WriteFile(wmic, []byte(wmicContent), 0o644); err != nil {
		t.Fatalf("write wmic bat: %v", err)
	}

	origGOOS := currentGOOS
	origPATH := os.Getenv("PATH")
	currentGOOS = "windows"
	os.Setenv("PATH", dir+string(os.PathListSeparator)+origPATH)
	t.Cleanup(func() {
		currentGOOS = origGOOS
		os.Setenv("PATH", origPATH)
	})

	got := physicalUUID(context.Background())
	if got != "4C4C4544-0030-3010-8043-B4C04F4E4D32" {
		t.Fatalf("physicalUUID = %q", got)
	}
}

// TestWriteStateCreateTempDenied 覆盖 Manager.writeState 在目标目录不可写时 CreateTemp 失败的路径。
// 使用 C:\ 根目录作为 StateFile 路径（Windows 下非管理员无法写入）。
func TestWriteStateCreateTempDenied(t *testing.T) {
	// 先探测 C:\ 是否真的不可写；若可写则跳过。
	probe, err := os.CreateTemp(`C:\`, ".goframe-license-probe-*")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("C:\\ is writable on this machine; cannot trigger CreateTemp failure")
	}

	m := &Manager{opts: Options{StateFile: filepath.Join(`C:\`, "goframe-license-state-test.json")}}
	if err := m.writeState(stateData{LicenseID: "x", LastSeen: time.Now()}); err == nil {
		t.Fatal("expected writeState to fail on C:\\ root")
	}
}

// 说明：fingerprint.go:155/165/177/180（stableMAC 各分支）依赖宿主机网卡配置，
// 无法在测试中注入：本机有物理网卡且 MAC 全局唯一，primary 非空，故 177/180 不可达。
