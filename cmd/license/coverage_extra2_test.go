package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 说明：以下残留块经分析在本环境（Windows，非管理员）逻辑不可达，保留源码并在此注释：
//   - main.go:29（main 函数体）：go test 生成的测试二进制不执行本包 main；
//     若调用 main() 会触发 os.Exit 直接终止测试进程，无法纳入覆盖率。
//   - main.go:101（keygen 生成密钥对失败）：license.GenerateKeyPair 基于
//     crypto/rand，本地环境不会失败。
//   - main.go:315（revoke 签发吊销列表失败）：SignRevocation 仅在列表为 nil、
//     密钥长度非法或 JSON 编码失败时报错，此路径均已被前置校验排除。

// TestSubcommandBadFlagPaths 覆盖各子命令 flag 解析失败后的返回码分支。
func TestSubcommandBadFlagPaths(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"fingerprint", []string{"fingerprint", "-badflag"}},
		{"issue", []string{"issue", "-badflag"}},
		{"inspect", []string{"inspect", "-badflag"}},
		{"revoke", []string{"revoke", "-badflag"}},
	}
	for _, tc := range cases {
		code, _, stderr := runCLI(t, tc.args...)
		if code != 2 {
			t.Fatalf("%s bad flag code = %d, stderr=%q", tc.name, code, stderr)
		}
		if !strings.Contains(stderr, "Usage:") {
			t.Fatalf("%s bad flag stderr missing usage: %q", tc.name, stderr)
		}
	}
}

// TestIssueValidateFailure 覆盖 issue 因许可内容校验失败（expires 早于 not-before）返回错误的路径。
func TestIssueValidateFailure(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "private.pem")
	if _, _, stderr := runCLI(t, "keygen", "-dir", dir); stderr != "" {
		t.Fatalf("keygen stderr = %q", stderr)
	}
	code, stdout, stderr := runCLI(t, "issue",
		"-key", keyPath,
		"-id", "L-BAD",
		"-product", "goframe",
		"-not-before", "2021-01-01T00:00:00Z",
		"-expires", "2020-01-01T00:00:00Z",
		"-out", filepath.Join(dir, "license.json"),
	)
	if code != 1 {
		t.Fatalf("issue invalid range code = %d, stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "issue license") {
		t.Fatalf("issue invalid range stderr = %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "license.json")); !os.IsNotExist(err) {
		t.Fatalf("license file should not exist")
	}
}
