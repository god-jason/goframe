package sysinfo

import (
	"testing"

	"github.com/god-jason/goframe/api"
)

// TestRegisterTrailingSlashRoute 验证注册到带结尾斜杠路径上的采集闭包
// 与精确路径行为一致，均可直接调用。
func TestRegisterTrailingSlashRoute(t *testing.T) {
	api.Reset()
	register(testConfig())
	engine := mountTestEngine(t)

	var snapshot envelope[Snapshot]
	getJSON(t, engine, "/system/", &snapshot)
	if snapshot.Code != 0 {
		t.Fatalf("snapshot code = %d, message = %q", snapshot.Code, snapshot.Message)
	}
}
