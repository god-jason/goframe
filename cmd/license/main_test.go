package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunUsageAndDispatch(t *testing.T) {
	if code, _, stderr := runCLI(t); code != 2 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("no args = %d, %q", code, stderr)
	}
	if code, _, stderr := runCLI(t, "help"); code != 0 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("help = %d, %q", code, stderr)
	}
	if code, _, stderr := runCLI(t, "--help"); code != 0 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("--help = %d, %q", code, stderr)
	}
	code, _, stderr := runCLI(t, "bogus")
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown = %d, %q", code, stderr)
	}
	if code, _, _ := runCLI(t, "keygen", "-badflag"); code != 2 {
		t.Fatalf("bad flag = %d", code)
	}
	if code, _, _ := runCLI(t, "keygen", "-h"); code != 0 {
		t.Fatalf("subcommand help = %d", code)
	}
}

func TestKeygenIssueInspectRevokeFlow(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := runCLI(t, "keygen", "-dir", dir)
	if code != 0 {
		t.Fatalf("keygen = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "private key:") {
		t.Fatalf("keygen output = %q", stdout)
	}
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	for _, path := range []string{privatePath, publicPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}

	licensePath := filepath.Join(dir, "license.json")
	code, stdout, stderr = runCLI(t, "issue",
		"-key", privatePath,
		"-id", "L-CLI",
		"-product", "goframe",
		"-customer", "ACME",
		"-edition", "enterprise",
		"-features", "sso, audit",
		"-limits", "users=100,devices=-1",
		"-machines", "fp-1",
		"-metadata", "order=PO-1,broken",
		"-issued-at", "2020-01-01T00:00:00Z",
		"-not-before", "2020-01-01T00:00:00Z",
		"-expires", "2035-01-01T00:00:00Z",
		"-grace", "1h",
		"-out", licensePath,
	)
	if code != 0 {
		t.Fatalf("issue = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "license written:") || !strings.Contains(stdout, "expires:") {
		t.Fatalf("issue output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "inspect", "-public", publicPath, "-license", licensePath, "-fingerprint", "fp-1")
	if code != 0 {
		t.Fatalf("inspect = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "status: valid") || !strings.Contains(stdout, "valid: true") {
		t.Fatalf("inspect output = %q", stdout)
	}

	// 未显式指定指纹时会自动采集；许可绑定 fp-1，通常与当前机器不匹配。
	code, stdout, _ = runCLI(t, "inspect", "-public", publicPath, "-license", licensePath)
	if code != 1 || !strings.Contains(stdout, "status: machine_mismatch") {
		t.Fatalf("inspect auto fingerprint = %d, %q", code, stdout)
	}

	revocationPath := filepath.Join(dir, "revocation.json")
	code, stdout, stderr = runCLI(t, "revoke", "-key", privatePath, "-ids", "L-CLI,L-OTHER",
		"-reasons", "L-CLI=refund", "-out", revocationPath)
	if code != 0 {
		t.Fatalf("revoke = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "revoked: 2") {
		t.Fatalf("revoke output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "inspect", "-public", publicPath, "-license", licensePath,
		"-fingerprint", "fp-1", "-revocation", revocationPath)
	if code != 1 {
		t.Fatalf("inspect revoked = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "status: revoked") || !strings.Contains(stdout, "reason:") {
		t.Fatalf("revoked output = %q", stdout)
	}

	// 过期且无宽限期的许可返回非零退出码。
	expiredPath := filepath.Join(dir, "expired.json")
	if code, _, stderr := runCLI(t, "issue", "-key", privatePath, "-id", "L-OLD", "-product", "goframe",
		"-issued-at", "2020-01-01T00:00:00Z", "-expires", "2021-01-01T00:00:00Z", "-out", expiredPath); code != 0 {
		t.Fatalf("issue expired = %d, %q", code, stderr)
	}
	if code, stdout, _ := runCLI(t, "inspect", "-public", publicPath, "-license", expiredPath, "-fingerprint", "fp-1"); code != 1 {
		t.Fatalf("inspect expired = %d, %q", code, stdout)
	}
}

func TestKeygenWriteErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if code, _, _ := runCLI(t, "keygen", "-dir", blocker); code != 1 {
		t.Fatalf("keygen blocked dir = %d", code)
	}
	if code, _, _ := runCLI(t, "keygen", "-dir", dir, "-private", filepath.Join("missing", "private.pem")); code != 1 {
		t.Fatalf("keygen private write = %d", code)
	}
	if code, _, _ := runCLI(t, "keygen", "-dir", dir, "-public", filepath.Join("missing", "public.pem")); code != 1 {
		t.Fatalf("keygen public write = %d", code)
	}
}

func TestIssueErrors(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runCLI(t, "keygen", "-dir", dir)
	if code != 0 {
		t.Fatalf("keygen = %d, %q", code, stderr)
	}
	privatePath := filepath.Join(dir, "private.pem")

	if code, _, _ := runCLI(t, "issue"); code != 2 {
		t.Fatalf("missing args = %d", code)
	}
	if code, _, _ := runCLI(t, "issue", "-key", filepath.Join(dir, "missing.pem"), "-id", "L", "-product", "p"); code != 1 {
		t.Fatalf("missing key = %d", code)
	}

	badKey := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badKey, []byte("not pem"), 0o600); err != nil {
		t.Fatalf("write bad key: %v", err)
	}
	if code, _, _ := runCLI(t, "issue", "-key", badKey, "-id", "L", "-product", "p"); code != 1 {
		t.Fatalf("bad key = %d", code)
	}

	base := []string{"issue", "-key", privatePath, "-id", "L", "-product", "p"}
	if code, _, _ := runCLI(t, append(base, "-limits", "users")...); code != 2 {
		t.Fatalf("invalid limit format = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-limits", "users=abc")...); code != 1 {
		t.Fatalf("invalid limit value = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-issued-at", "yesterday")...); code != 1 {
		t.Fatalf("invalid issued-at = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-expires", "yesterday")...); code != 1 {
		t.Fatalf("invalid expires = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-not-before", "yesterday")...); code != 1 {
		t.Fatalf("invalid not-before = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-out", dir)...); code != 1 {
		t.Fatalf("write license to dir = %d", code)
	}
	if code, _, _ := runCLI(t, append(base, "-product", "", "-out", filepath.Join(dir, "l.json"))...); code != 2 {
		t.Fatalf("empty product = %d", code)
	}
}

func TestInspectErrors(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runCLI(t, "keygen", "-dir", dir)
	if code != 0 {
		t.Fatalf("keygen = %d, %q", code, stderr)
	}
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")
	licensePath := filepath.Join(dir, "license.json")
	if code, _, stderr := runCLI(t, "issue", "-key", privatePath, "-id", "L", "-product", "p", "-out", licensePath); code != 0 {
		t.Fatalf("issue = %d, %q", code, stderr)
	}

	if code, _, _ := runCLI(t, "inspect", "-public", filepath.Join(dir, "missing.pem"), "-license", licensePath); code != 1 {
		t.Fatalf("missing public = %d", code)
	}
	badPublic := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badPublic, []byte("not pem"), 0o600); err != nil {
		t.Fatalf("write bad public: %v", err)
	}
	if code, _, _ := runCLI(t, "inspect", "-public", badPublic, "-license", licensePath); code != 1 {
		t.Fatalf("bad public = %d", code)
	}
	if code, _, _ := runCLI(t, "inspect", "-public", publicPath, "-license", filepath.Join(dir, "missing.json")); code != 1 {
		t.Fatalf("missing license = %d", code)
	}
	badLicense := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badLicense, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write bad license: %v", err)
	}
	if code, _, _ := runCLI(t, "inspect", "-public", publicPath, "-license", badLicense); code != 1 {
		t.Fatalf("bad license = %d", code)
	}
	if code, _, _ := runCLI(t, "inspect", "-public", publicPath, "-license", licensePath,
		"-revocation", filepath.Join(dir, "missing-revocation.json")); code != 1 {
		t.Fatalf("missing revocation = %d", code)
	}
	badRevocation := filepath.Join(dir, "bad-revocation.json")
	if err := os.WriteFile(badRevocation, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write bad revocation: %v", err)
	}
	if code, _, _ := runCLI(t, "inspect", "-public", publicPath, "-license", licensePath,
		"-revocation", badRevocation); code != 1 {
		t.Fatalf("bad revocation = %d", code)
	}
}

func TestRevokeErrors(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runCLI(t, "keygen", "-dir", dir)
	if code != 0 {
		t.Fatalf("keygen = %d, %q", code, stderr)
	}
	privatePath := filepath.Join(dir, "private.pem")

	if code, _, _ := runCLI(t, "revoke"); code != 2 {
		t.Fatalf("missing ids = %d", code)
	}
	if code, _, _ := runCLI(t, "revoke", "-key", filepath.Join(dir, "missing.pem"), "-ids", "L-1"); code != 1 {
		t.Fatalf("missing key = %d", code)
	}
	badKey := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badKey, []byte("not pem"), 0o600); err != nil {
		t.Fatalf("write bad key: %v", err)
	}
	if code, _, _ := runCLI(t, "revoke", "-key", badKey, "-ids", "L-1"); code != 1 {
		t.Fatalf("bad key = %d", code)
	}
	if code, _, _ := runCLI(t, "revoke", "-key", privatePath, "-ids", "L-1", "-out", dir); code != 1 {
		t.Fatalf("write revocation to dir = %d", code)
	}
}

func TestFingerprintCommand(t *testing.T) {
	code, stdout, stderr := runCLI(t, "fingerprint", "-sources", "hostid")
	if code != 0 {
		t.Fatalf("fingerprint = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "fingerprint:") || !strings.Contains(stdout, "source:") {
		t.Fatalf("fingerprint output = %q", stdout)
	}
	if code, _, _ := runCLI(t, "fingerprint", "-sources", "unknown"); code != 1 {
		t.Fatalf("unknown source = %d", code)
	}
}
