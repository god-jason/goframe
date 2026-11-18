package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLicenseHelperMethods(t *testing.T) {
	var nilLicense *License
	if nilLicense.GracePeriod() != 0 {
		t.Fatal("nil grace period must be zero")
	}
	if _, ok := nilLicense.ExpiresWithGrace(); ok {
		t.Fatal("nil license must not have expiration")
	}
	if nilLicense.HasFeature("sso") {
		t.Fatal("nil license has no features")
	}
	if _, ok := nilLicense.Limit("users"); ok {
		t.Fatal("nil license has no limits")
	}
	if nilLicense.Clone() != nil {
		t.Fatal("nil clone must be nil")
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	notBefore := now.Add(-time.Hour)
	license := &License{
		GracePeriodSeconds: 90,
		ExpiresAt:          &expires,
		NotBefore:          &notBefore,
		Features:           []string{"*"},
		Limits:             map[string]int64{"users": 10},
		Machines:           []string{"fp"},
		Metadata:           map[string]string{"order": "1"},
	}
	if license.GracePeriod() != 90*time.Second {
		t.Fatalf("grace = %s", license.GracePeriod())
	}
	withGrace, ok := license.ExpiresWithGrace()
	if !ok || !withGrace.Equal(expires.Add(90*time.Second)) {
		t.Fatalf("expires with grace = %s, %v", withGrace, ok)
	}
	if !license.HasFeature("anything") || !license.HasFeature(" * ") {
		t.Fatal("wildcard feature must allow everything")
	}
	if limit, ok := license.Limit("users"); !ok || limit != 10 {
		t.Fatalf("limit = %d, %v", limit, ok)
	}
	if _, ok := license.Limit("missing"); ok {
		t.Fatal("unexpected limit")
	}

	clone := license.Clone()
	clone.Features[0] = "changed"
	clone.Machines[0] = "changed"
	clone.Limits["users"] = 99
	clone.Metadata["order"] = "2"
	*clone.ExpiresAt = now
	if license.Features[0] != "*" || license.Machines[0] != "fp" ||
		license.Limits["users"] != 10 || license.Metadata["order"] != "1" ||
		!license.ExpiresAt.Equal(expires) {
		t.Fatal("Clone must deep copy all mutable fields")
	}

	// 无到期时间与无宽限期。
	permanent := &License{}
	if _, ok := permanent.ExpiresWithGrace(); ok {
		t.Fatal("license without expiration must return false")
	}
	if permanent.GracePeriod() != 0 {
		t.Fatal("zero grace period expected")
	}
}

func TestEvaluateStatuses(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if status, reason := Evaluate(nil, now, "fp", nil); status != StatusExpired || reason == "" {
		t.Fatalf("nil license = %s, %q", status, reason)
	}

	revoked := &License{ID: "L-1"}
	if status, _ := Evaluate(revoked, now, "fp", &RevocationList{IDs: []string{"L-1"}}); status != StatusRevoked {
		t.Fatalf("status = %s", status)
	}

	bound := &License{ID: "L-2", Machines: []string{"other"}}
	if status, _ := Evaluate(bound, now, "fp", nil); status != StatusMachineMismatch {
		t.Fatalf("status = %s", status)
	}

	wildcard := &License{ID: "L-3", Machines: []string{"*"}}
	if status, _ := Evaluate(wildcard, now, "", nil); status != StatusValid {
		t.Fatalf("status = %s", status)
	}

	caseInsensitive := &License{ID: "L-4", Machines: []string{" FP "}}
	if status, _ := Evaluate(caseInsensitive, now, "fp", nil); status != StatusValid {
		t.Fatalf("status = %s", status)
	}

	notBefore := now.Add(time.Hour)
	future := &License{ID: "L-5", NotBefore: &notBefore}
	if status, reason := Evaluate(future, now, "", nil); status != StatusNotYetValid || reason == "" {
		t.Fatalf("status = %s, %q", status, reason)
	}

	expires := now.Add(time.Hour)
	valid := &License{ID: "L-6", ExpiresAt: &expires}
	if status, reason := Evaluate(valid, now, "", nil); status != StatusValid || reason != "" {
		t.Fatalf("status = %s, %q", status, reason)
	}

	graceExpires := now.Add(-time.Minute)
	grace := &License{ID: "L-7", ExpiresAt: &graceExpires, GracePeriodSeconds: 3600}
	if status, _ := Evaluate(grace, now, "", nil); status != StatusGrace {
		t.Fatalf("status = %s", status)
	}

	expired := &License{ID: "L-8", ExpiresAt: &graceExpires}
	if status, reason := Evaluate(expired, now, "", nil); status != StatusExpired || reason == "" {
		t.Fatalf("status = %s, %q", status, reason)
	}

	permanent := &License{ID: "L-9"}
	if status, _ := Evaluate(permanent, now, "", nil); status != StatusValid {
		t.Fatalf("status = %s", status)
	}

	for _, status := range []Status{StatusValid, StatusGrace} {
		if !status.Valid() {
			t.Fatalf("%s should be valid", status)
		}
	}
	for _, status := range []Status{StatusExpired, StatusNotYetValid, StatusRevoked, StatusMachineMismatch, StatusClockRollback} {
		if status.Valid() {
			t.Fatalf("%s should not be valid", status)
		}
	}
}

func TestLicenseValidate(t *testing.T) {
	now := time.Now()
	notBefore := now
	expires := now.Add(-time.Hour)

	cases := []License{
		{Product: "p", IssuedAt: now},
		{ID: "id", IssuedAt: now},
		{ID: "id", Product: "p"},
		{ID: "id", Product: "p", IssuedAt: now, NotBefore: &notBefore, ExpiresAt: &expires},
	}
	for i := range cases {
		if err := cases[i].validate(); err == nil {
			t.Fatalf("case %d should be invalid", i)
		}
	}
	valid := License{ID: "id", Product: "p", IssuedAt: now}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid license rejected: %v", err)
	}
}

func TestPEMParsingErrors(t *testing.T) {
	if _, err := ParsePublicKeyPEM([]byte("not pem")); err == nil {
		t.Fatal("expected public key PEM error")
	}
	if _, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("bad")})); err == nil {
		t.Fatal("expected public key DER error")
	}
	if _, err := ParsePrivateKeyPEM([]byte("not pem")); err == nil {
		t.Fatal("expected private key PEM error")
	}
	if _, err := ParsePrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("bad")})); err == nil {
		t.Fatal("expected private key DER error")
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal rsa public: %v", err)
	}
	if _, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})); err == nil {
		t.Fatal("expected non-ed25519 public key error")
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("marshal rsa private: %v", err)
	}
	if _, err := ParsePrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})); err == nil {
		t.Fatal("expected non-ed25519 private key error")
	}
}

func TestSignLicenseErrors(t *testing.T) {
	_, privateKey := testKeyPair(t)

	if _, err := SignLicense(nil, privateKey); err == nil {
		t.Fatal("expected nil license error")
	}
	if _, err := SignLicense(&License{}, ed25519.PrivateKey("short")); err == nil {
		t.Fatal("expected invalid private key error")
	}
	if _, err := SignLicense(&License{}, privateKey); err == nil {
		t.Fatal("expected validation error")
	}

	content, license, err := Issue(IssueOptions{ID: "L-1", Product: "p"}, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if license.IssuedAt.IsZero() || content == nil {
		t.Fatal("Issue must default issued_at")
	}
	if _, _, err := Issue(IssueOptions{Product: "p"}, privateKey); err == nil {
		t.Fatal("expected issue validation error")
	}
}

func TestVerifyLicenseDecodeErrors(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	_, otherKey := testKeyPair(t)
	now := time.Now()

	if _, err := VerifyLicense([]byte("{}"), ed25519.PublicKey("short")); err == nil {
		t.Fatal("expected invalid public key error")
	}
	if _, err := VerifyLicense([]byte("not json"), publicKey); err == nil {
		t.Fatal("expected decode error")
	}

	cases := map[string]envelope{
		"unsupported version": {Version: 99},
		"invalid payload":     {Version: envelopeVersion, Payload: "!!!", Signature: base64.StdEncoding.EncodeToString([]byte("sig"))},
		"invalid signature":   {Version: envelopeVersion, Payload: base64.StdEncoding.EncodeToString([]byte(`{}`)), Signature: "!!!"},
	}
	for name, file := range cases {
		content, err := json.Marshal(file)
		if err != nil {
			t.Fatalf("%s: marshal envelope: %v", name, err)
		}
		if _, err := VerifyLicense(content, publicKey); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}

	// 签名正确但载荷不是 JSON。
	if _, err := VerifyLicense(signedEnvelope(t, []byte("not-json"), privateKey), publicKey); err == nil {
		t.Fatal("expected license decode error")
	}

	// 签名正确但许可字段非法。
	invalidPayload, err := json.Marshal(License{Product: "p", IssuedAt: now})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	if _, err := VerifyLicense(signedEnvelope(t, invalidPayload, privateKey), publicKey); err == nil {
		t.Fatal("expected license validation error")
	}

	// 使用另一把私钥签名。
	validPayload, err := json.Marshal(License{ID: "L-1", Product: "p", IssuedAt: now})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	if _, err := VerifyLicense(signedEnvelope(t, validPayload, otherKey), publicKey); err == nil {
		t.Fatal("expected signature mismatch")
	}
	if _, err := VerifyLicense(signedEnvelope(t, validPayload, privateKey), publicKey); err != nil {
		t.Fatalf("expected valid license: %v", err)
	}
}

func signedEnvelope(t *testing.T, payload []byte, privateKey ed25519.PrivateKey) []byte {
	t.Helper()
	file := envelope{
		Version:   envelopeVersion,
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
	}
	content, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return content
}

func TestRevocationErrors(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	_, otherKey := testKeyPair(t)

	var nilList *RevocationList
	if nilList.Contains("L-1") {
		t.Fatal("nil list must not contain ids")
	}
	list := &RevocationList{IDs: []string{"L-1"}}
	if !list.Contains(" L-1 ") || list.Contains("L-2") {
		t.Fatalf("unexpected Contains result")
	}
	trimmed := &RevocationList{IDs: []string{"L-1"}}
	if !trimmed.Contains(" L-1 ") {
		t.Fatal("expected trimmed match")
	}

	if _, err := SignRevocation(nil, privateKey); err == nil {
		t.Fatal("expected nil list error")
	}
	if _, err := SignRevocation(&RevocationList{}, ed25519.PrivateKey("short")); err == nil {
		t.Fatal("expected invalid key error")
	}

	content, err := SignRevocation(&RevocationList{IDs: []string{"L-1"}}, privateKey)
	if err != nil {
		t.Fatalf("SignRevocation: %v", err)
	}
	if _, err := VerifyRevocation(content, ed25519.PublicKey("short")); err == nil {
		t.Fatal("expected invalid public key error")
	}
	if _, err := VerifyRevocation([]byte("not json"), publicKey); err == nil {
		t.Fatal("expected decode error")
	}

	cases := map[string]envelope{
		"unsupported version": {Version: 99},
		"invalid payload":     {Version: envelopeVersion, Payload: "!!!", Signature: base64.StdEncoding.EncodeToString([]byte("sig"))},
		"invalid signature":   {Version: envelopeVersion, Payload: base64.StdEncoding.EncodeToString([]byte(`{}`)), Signature: "!!!"},
	}
	for name, file := range cases {
		raw, err := json.Marshal(file)
		if err != nil {
			t.Fatalf("%s: marshal envelope: %v", name, err)
		}
		if _, err := VerifyRevocation(raw, publicKey); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}

	if _, err := VerifyRevocation(signedEnvelope(t, []byte("not-json"), privateKey), publicKey); err == nil {
		t.Fatal("expected revocation decode error")
	}
	if _, err := VerifyRevocation(signedEnvelope(t, []byte(`{"ids":["L-1"]}`), otherKey), publicKey); err == nil {
		t.Fatal("expected signature error")
	}
	verified, err := VerifyRevocation(signedEnvelope(t, []byte(`{"ids":["L-1"]}`), privateKey), publicKey)
	if err != nil {
		t.Fatalf("VerifyRevocation: %v", err)
	}
	if !verified.Contains("L-1") {
		t.Fatal("expected revoked id")
	}
}

func TestManagerNilReceiverAndDefaults(t *testing.T) {
	var manager *Manager
	if err := manager.Reload(); err == nil {
		t.Fatal("expected nil manager reload error")
	}
	if manager.Snapshot().Status != StatusExpired {
		t.Fatal("nil snapshot must be expired")
	}
	if manager.Status() != StatusExpired || manager.Valid() || manager.InGrace() {
		t.Fatal("nil manager status must be expired")
	}
	if manager.License() != nil {
		t.Fatal("nil manager license must be nil")
	}
	if manager.HasFeature("sso") || !manager.WithinLimit("users", 1) {
		t.Fatal("nil manager must deny features and allow limits")
	}
	if _, ok := manager.Limit("users"); ok {
		t.Fatal("nil manager must not report limits")
	}

	publicKey, privateKey := testKeyPair(t)
	now := time.Now()
	content, _, err := Issue(IssueOptions{ID: "L-1", Product: "p", IssuedAt: now}, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	manager, err = New(Options{PublicKey: publicKey, LicenseData: content})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if manager.Status() != StatusValid {
		t.Fatalf("status = %s", manager.Status())
	}
	if manager.License() == nil || manager.License().ID != "L-1" {
		t.Fatal("expected license copy")
	}
	if !manager.WithinLimit("missing", 0) {
		t.Fatal("unconfigured limit must be unlimited")
	}
}

func TestManagerNewErrors(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	now := time.Now()
	content, _, err := Issue(IssueOptions{ID: "L-1", Product: "p", IssuedAt: now}, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := New(Options{PublicKey: ed25519.PublicKey("short"), LicenseData: content}); err == nil {
		t.Fatal("expected invalid public key error")
	}
	if _, err := New(Options{PublicKey: publicKey}); err == nil {
		t.Fatal("expected missing license error")
	}
	if _, err := New(Options{PublicKey: publicKey, LicenseFile: filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("expected read license error")
	}
	if _, err := New(Options{PublicKey: publicKey, LicenseData: content, StateFile: t.TempDir()}); err == nil {
		t.Fatal("expected state read error")
	}
}

func TestManagerStateFileAndReloadErrors(t *testing.T) {
	publicKey, privateKey := testKeyPair(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	content, _, err := Issue(IssueOptions{ID: "L-1", Product: "p", IssuedAt: now}, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	if err := os.WriteFile(stateFile, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if _, err := New(Options{PublicKey: publicKey, LicenseData: content, StateFile: stateFile}); err == nil {
		t.Fatal("expected invalid state error")
	}

	// 状态文件未来时间但仍在容差内时不视为回拨。
	later := now.Add(time.Minute)
	rawState, err := json.Marshal(stateData{LicenseID: "L-1", LastSeen: later})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(stateFile, rawState, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	manager, err := New(Options{
		PublicKey:      publicKey,
		LicenseData:    content,
		StateFile:      stateFile,
		Now:            func() time.Time { return now },
		ClockTolerance: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if manager.Status() != StatusValid {
		t.Fatalf("status = %s", manager.Status())
	}

	// 状态文件目录不可创建。
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := New(Options{
		PublicKey:   publicKey,
		LicenseData: content,
		StateFile:   filepath.Join(blocker, "state.json"),
		Now:         func() time.Time { return now },
	}); err == nil {
		t.Fatal("expected state write error")
	}

	// Reload 失败时保留旧状态。
	licensePath := filepath.Join(dir, "license.json")
	if err := os.WriteFile(licensePath, content, 0o600); err != nil {
		t.Fatalf("write license: %v", err)
	}
	manager, err = New(Options{
		PublicKey:   publicKey,
		LicenseFile: licensePath,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.WriteFile(licensePath, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write broken license: %v", err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	if manager.Status() != StatusValid {
		t.Fatalf("old status must be preserved, got %s", manager.Status())
	}
}

func TestFingerprintHelpers(t *testing.T) {
	settings := map[string]string{
		`" 4c4c4544-0030-3010-8043-b4c04f4e4d32 "`:    "4C4C4544-0030-3010-8043-B4C04F4E4D32",
		"4c4c4544-0030-3010-8043-b4c04f4e4d32":        "4C4C4544-0030-3010-8043-B4C04F4E4D32",
		"line1\n4C4C4544-0030-3010-8043-B4C04F4E4D32": "4C4C4544-0030-3010-8043-B4C04F4E4D32",
		"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF":        "",
		"03000200-0400-0500-0006-000700080009":        "",
		"4c4c4544-0030-3010-8043-b4c04f4e4d3":         "",
		"4c4c4544-0030-3010-8043-b4c04f4e4d32x":       "",
	}
	for input, want := range settings {
		if got := normalizeUUID(input); got != want {
			t.Fatalf("normalizeUUID(%q) = %q, want %q", input, got, want)
		}
	}

	if got := parseIOPlatformUUID(`"IOPlatformUUID" = "12345678-1234-1234-1234-1234567890AB`); got != "12345678-1234-1234-1234-1234567890AB" {
		t.Fatalf("unterminated uuid = %q", got)
	}
	if got := parseIOPlatformUUID(`"IOPlatformUUID" = "not-a-uuid"`); got != "" {
		t.Fatalf("invalid uuid = %q", got)
	}
	if got := parseIOPlatformUUID(`"IOPlatformUUID" = "12345678-1234-1234-1234-1234567890AB"`); got != "12345678-1234-1234-1234-1234567890AB" {
		t.Fatalf("uuid = %q", got)
	}

	if got := commandOutput(nil, "go", "version"); !strings.Contains(got, "go version") {
		t.Fatalf("commandOutput = %q", got)
	}
	if got := commandOutput(context.Background(), "goframe-command-that-does-not-exist"); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := systemHostID(ctx); got == "" {
		t.Log("host id unavailable with canceled context")
	}
	// Windows 分支即使命令失败也应安全返回空值。
	if got := diskSerial(ctx); got != "" {
		t.Fatalf("expected empty disk serial with canceled context, got %q", got)
	}
	if got := physicalUUID(ctx); got != "" {
		t.Fatalf("expected empty physical uuid with canceled context, got %q", got)
	}

	if _, err := CollectFingerprint(context.Background(), FingerprintOptions{Sources: []string{"unknown"}}); err == nil {
		t.Fatal("expected unknown source error")
	}

	fingerprint, err := Fingerprint(context.Background())
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if len(fingerprint) != 64 {
		t.Fatalf("fingerprint = %q", fingerprint)
	}

	if mac := stableMAC(); mac == "" {
		t.Log("no stable MAC available on this host")
	}
}

func TestPhysicalUUIDPlatformBranches(t *testing.T) {
	originalGOOS := currentGOOS
	originalRead := readFileFn
	t.Cleanup(func() {
		currentGOOS = originalGOOS
		readFileFn = originalRead
	})

	uuid := "4c4c4544-0030-3010-8043-b4c04f4e4d32"
	currentGOOS = "linux"
	readFileFn = func(path string) ([]byte, error) {
		return []byte(uuid), nil
	}
	if got := physicalUUID(context.Background()); got != "4C4C4544-0030-3010-8043-B4C04F4E4D32" {
		t.Fatalf("linux uuid = %q", got)
	}

	// 所有文件读取失败时不返回 UUID。
	readFileFn = func(path string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	if got := physicalUUID(context.Background()); got != "" {
		t.Fatalf("linux missing uuid = %q", got)
	}

	currentGOOS = "plan9"
	if got := physicalUUID(context.Background()); got != "" {
		t.Fatalf("unsupported platform uuid = %q", got)
	}
}
