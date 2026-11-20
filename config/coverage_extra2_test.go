package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 说明：以下残留块经分析在本环境（Windows）逻辑不可达，保留源码并在此注释：
//   - registry.go:264（filepath.Abs 失败）：仅当 os.Getwd 失败且路径为相对路径时出错；
//     Windows 下进程无法删除自身工作目录，Getwd 不会失败。
//   - registry.go:268（fsnotify.NewWatcher 失败）：Windows 后端仅创建 IOCP 句柄，本地环境不会失败。
//   - registry.go:291/307（Events/Errors 通道被关闭）：通道只在 watcher.Close 时关闭，
//     而 Close 由 watchSections 自身 defer 调用，循环运行期间不可能观察到关闭。
//   - registry.go:306/309（Errors 通道收到错误）：fsnotify Windows 后端仅在
//     ReadDirectoryChangesW 完成状态出现非常规错误时上报；目录被删除走事件通道而非错误通道，
//     缓冲区溢出依赖竞态，无法确定性构造。
//   - section.go:71（NewDecoder 失败）：Result 恒为可取址指针且 DecodeHook 合法，mapstructure 不会返回错误。

// TestCoverageNullDocument 覆盖 YAML 文档为 null 时 loadSections 的空表兜底分支。
func TestCoverageNullDocument(t *testing.T) {
	type nullSection struct {
		Name string `yaml:"name"`
	}
	section := Register("coverage.nullsec", nullSection{Name: "fallback"})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// 子节点显式为 null，顶层映射保持非空。
	if err := os.WriteFile(path, []byte("coverage:\n  nullsec: null\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	SetFile(path)
	defer SetFile("")
	if err := Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := section.Get().Name; got != "fallback" {
		t.Fatalf("null document name = %q", got)
	}

	// 顶层文档整体为 null，触发 raw == nil 兜底。
	if err := os.WriteFile(path, []byte("null\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Reload(); err != nil {
		t.Fatalf("reload null: %v", err)
	}
	if got := section.Get().Name; got != "fallback" {
		t.Fatalf("top-level null name = %q", got)
	}
}

// TestCoverageLowerKeysList 覆盖 lowerKeys 对 YAML 列表内嵌映射的递归小写分支。
func TestCoverageLowerKeysList(t *testing.T) {
	type server struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}
	type listSection struct {
		Servers []server `yaml:"servers"`
	}
	section := Register("coverage.listsec", listSection{})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "coverage:\n  listsec:\n    servers:\n      - NAME: alpha\n        PORT: 8080\n      - NAME: beta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	SetFile(path)
	defer SetFile("")
	if err := Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	servers := section.Get().Servers
	if len(servers) != 2 {
		t.Fatalf("servers len = %d", len(servers))
	}
	if servers[0].Name != "alpha" || servers[0].Port != 8080 {
		t.Fatalf("servers[0] = %+v", servers[0])
	}
	if servers[1].Name != "beta" {
		t.Fatalf("servers[1] = %+v", servers[1])
	}
}
