package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/config"
)

// zzWriteLifecycleConfig 生成一份可正常启动的配置文件，logger 输出到 stderr 避免残留文件句柄。
func zzWriteLifecycleConfig(t *testing.T, port int) string {
	t.Helper()
	dir := t.TempDir()
	storageDir, err := os.MkdirTemp("", "goframe-example-storage")
	if err != nil {
		t.Fatalf("mkdir storage: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storageDir) })
	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf(`app:
  name: example-test
  environment: test
logger:
  level: error
  format: json
  output: stderr
storage:
  driver: local
  path: %s
scheduler:
  enabled: false
pool:
  workers: 1
  queue_size: 4
server:
  host: 127.0.0.1
  port: %d
`, storageDir, port)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

// TestRunContextMountFailure 覆盖 serve 中 module.Mount 失败的分支。
// 通过临时破坏一个已登记接口的路径触发 api.Mount 的校验错误。
func TestRunContextMountFailure(t *testing.T) {
	configPath := zzWriteLifecycleConfig(t, freePort(t))

	api.RegisterPublic("GET", "/zz-mount-probe", func(c *gin.Context) {})
	var probe *api.API
	for _, entry := range api.Entries() {
		if entry.Path == "/zz-mount-probe" {
			probe = entry
		}
	}
	if probe == nil {
		t.Fatal("probe entry missing")
	}
	probe.Path = "zz-mount-probe" // 临时去掉前导斜杠，使挂载校验失败。
	t.Cleanup(func() { probe.Path = "/zz-mount-probe" })

	t.Cleanup(func() { config.SetFile("") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stderr bytes.Buffer
	code := runContext(ctx, []string{"-config", configPath}, io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("mount failure code = %d, stderr=%q", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("mount module apis:")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestMainServesForever 覆盖 main() 中的 os.Exit(run(...)) 语句。
// main() 在独立 goroutine 中运行：boot.Run 启动 HTTP 服务后永久阻塞，
// run 不返回、os.Exit 不执行，测试进程正常结束时泄漏的 goroutine 随进程退出。
// 该用例必须位于本包最后，避免残留的全局启动状态影响其他用例。
func TestMainServesForever(t *testing.T) {
	port := freePort(t)
	configPath := zzWriteLifecycleConfig(t, port)

	oldArgs := os.Args
	os.Args = []string{"example", "-config", configPath}
	go main()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	up := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			up = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.Args = oldArgs
	if !up {
		t.Fatal("main() server never started listening")
	}
}
