package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 说明：在 coverage_extra_test.go 已注释的不可达块之外，本环境（Windows，非管理员）
// 还存在以下无法覆盖的残留块，保留源码并在此注释：
//   - archive.go:195/235（file.Stat 失败）：对已成功打开的 *os.File 调用 Stat 走
//     GetFileInformationByHandle，常规文件与目录句柄均不会失败；设备名路径在 Open 阶段即失败。
//   - archive.go:454/668（文件 Close 失败）：Windows 上对有效句柄 CloseHandle 不会失败。
//   - archive.go:527/546/724（符号链接分支）：本机创建符号链接需要管理员权限，
//     coverage_extra_test.go 中相关用例已在 os.Symlink 失败时跳过。
//   - archive.go:550（filepath.Rel 失败）：Rel(src, current) 中 current 由 WalkDir 基于
//     同一 src 派生，恒可计算。
//   - archive.go:558（entry.Info 失败）：WalkDir 的 DirEntry 信息来自同一次 Lstat，不会失败。
//   - archive.go:765/768（safePath Rel/越界失败）：clean 已排除 ".." 前缀与绝对路径，
//     Rel(dest, Join(dest, clean)) 恒返回相对名。
//   - zip.go:62（entry Close 失败）：archive/zip 的 Open 返回读取器 Close 恒为 nil。
//   - zip.go:108（zip.FileInfoHeader 失败）：标准库该函数对任意 FileInfo 均返回 nil 错误。

// TestCompressAsCreateTempDenied 覆盖目标目录存在但临时文件创建失败的路径。
// Windows 下非管理员进程无法在 C:\ 根目录创建文件，而 MkdirAll 对已存在目录返回成功。
func TestCompressAsCreateTempDenied(t *testing.T) {
	probe, err := os.CreateTemp(`C:\`, ".goframe-archive-probe-*")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip(`C:\ 根目录可写，无法触发 CreateTemp 失败`)
	}

	root := t.TempDir()
	src := filepath.Join(root, "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	dest := filepath.Join(`C:\`, "goframe-archive-coverage.zip")
	err = CompressAs(dest, Zip, src)
	if err == nil || !strings.Contains(err.Error(), "create temp file") {
		t.Fatalf("expected create temp file error, got %v", err)
	}
}

// TestCollectSourcesWalkDenied 覆盖目录遍历过程中子目录读取失败的路径。
// 通过 icacls 对子目录拒绝当前用户读取，使 WalkDir 回调收到 walkErr。
func TestCollectSourcesWalkDenied(t *testing.T) {
	user := os.Getenv("USERNAME")
	if user == "" {
		t.Skip("USERNAME 为空，无法构造 ACL 拒绝")
	}
	if _, err := exec.LookPath("icacls"); err != nil {
		t.Skip("icacls 不可用")
	}

	root := t.TempDir()
	src := filepath.Join(root, "src")
	locked := filepath.Join(src, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	deny := exec.Command("icacls", locked, "/deny", user+":(OI)(CI)(RX)")
	if out, err := deny.CombinedOutput(); err != nil {
		t.Skipf("icacls 拒绝失败: %v: %s", err, out)
	}
	t.Cleanup(func() {
		// 恢复权限，确保临时目录可被清理。
		_ = exec.Command("icacls", locked, "/remove:d", user).Run()
	})

	dest := filepath.Join(root, "backup.zip")
	err := Compress(dest, src)
	if err == nil {
		t.Fatal("expected walk error for denied subdirectory")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Fatalf("error should mention denied directory, got %v", err)
	}
}
