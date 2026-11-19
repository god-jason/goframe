package license

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

func withLicenseConfig(t *testing.T, cfg Config) {
	t.Helper()
	previous := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = previous
		_ = Shutdown(context.Background())
	})
}

type licenseAssets struct {
	dir          string
	publicPath   string
	privateKey   ed25519.PrivateKey
	licensePath  string
	licenseBytes []byte
}

func writeLicenseAssets(t *testing.T, options IssueOptions) licenseAssets {
	t.Helper()
	dir := t.TempDir()
	publicPEM, privatePEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	publicKey, err := ParsePublicKeyPEM(publicPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}
	_ = publicKey
	privateKey, err := ParsePrivateKeyPEM(privatePEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}

	publicPath := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(publicPath, publicPEM, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	privatePath := filepath.Join(dir, "private.pem")
	if err := os.WriteFile(privatePath, privatePEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	content, _, err := Issue(options, privateKey)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	licensePath := filepath.Join(dir, "license.json")
	if err := os.WriteFile(licensePath, content, 0o600); err != nil {
		t.Fatalf("write license: %v", err)
	}
	return licenseAssets{
		dir:          dir,
		publicPath:   publicPath,
		privateKey:   privateKey,
		licensePath:  licensePath,
		licenseBytes: content,
	}
}

func TestBootDisabledAndMissingConfiguration(t *testing.T) {
	withLicenseConfig(t, Config{Enabled: false})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("disabled startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("disabled license must not create a manager")
	}

	withLicenseConfig(t, Config{Enabled: true})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected missing file error")
	}

	assets := writeLicenseAssets(t, IssueOptions{ID: "L-1", Product: "p", IssuedAt: time.Now()})
	withLicenseConfig(t, Config{Enabled: true, File: assets.licensePath})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected missing public key error")
	}

	withLicenseConfig(t, Config{Enabled: true, File: assets.licensePath, PublicKey: "-----BEGIN PUBLIC KEY-----\nbroken\n-----END PUBLIC KEY-----"})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected invalid public key error")
	}

	withLicenseConfig(t, Config{
		Enabled:   true,
		File:      assets.licensePath,
		PublicKey: filepath.Join(assets.dir, "missing-public.pem"),
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected public key read error")
	}
}

func TestBootLoadAndRevocationErrors(t *testing.T) {
	assets := writeLicenseAssets(t, IssueOptions{ID: "L-1", Product: "p", IssuedAt: time.Now()})

	// Strict 模式下许可读取失败会阻断启动。
	withLicenseConfig(t, Config{
		Enabled:   true,
		File:      filepath.Join(assets.dir, "missing.json"),
		PublicKey: assets.publicPath,
		Strict:    true,
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected strict startup failure")
	}

	// 非 Strict 模式继续以未授权状态运行。
	withLicenseConfig(t, Config{
		Enabled:   true,
		File:      filepath.Join(assets.dir, "missing.json"),
		PublicKey: assets.publicPath,
		Strict:    false,
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("non-strict startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("invalid license must not create a manager")
	}

	withLicenseConfig(t, Config{
		Enabled:        true,
		File:           assets.licensePath,
		PublicKey:      assets.publicPath,
		RevocationFile: filepath.Join(assets.dir, "missing-revocation.json"),
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected revocation read error")
	}

	invalidRevocation := filepath.Join(assets.dir, "bad-revocation.json")
	if err := os.WriteFile(invalidRevocation, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write revocation: %v", err)
	}
	withLicenseConfig(t, Config{
		Enabled:        true,
		File:           assets.licensePath,
		PublicKey:      assets.publicPath,
		RevocationFile: invalidRevocation,
	})
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected revocation verify error")
	}
}

func TestBootSuccessWithInlineKeyAndRevocation(t *testing.T) {
	now := time.Now()
	assets := writeLicenseAssets(t, IssueOptions{
		ID:        "L-success",
		Product:   "goframe",
		Edition:   "enterprise",
		IssuedAt:  now,
		Features:  []string{"sso"},
		ExpiresAt: timePointer(now.Add(time.Hour)),
	})

	publicPEM, err := os.ReadFile(assets.publicPath)
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	revocationPath := filepath.Join(assets.dir, "revocation.json")
	revocationBytes, err := SignRevocation(&RevocationList{IDs: []string{"other"}}, assets.privateKey)
	if err != nil {
		t.Fatalf("SignRevocation: %v", err)
	}
	if err := os.WriteFile(revocationPath, revocationBytes, 0o600); err != nil {
		t.Fatalf("write revocation: %v", err)
	}

	withLicenseConfig(t, Config{
		Enabled:        true,
		File:           assets.licensePath,
		PublicKey:      string(publicPEM),
		RevocationFile: revocationPath,
		Fingerprint:    "fp-1",
		StateFile:      filepath.Join(assets.dir, "state.json"),
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	manager := Get()
	if manager == nil || !manager.Valid() {
		t.Fatalf("manager = %+v", manager)
	}
	if !manager.HasFeature("sso") {
		t.Fatal("expected sso feature")
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("Shutdown must clear manager")
	}
}

func TestBootAutoFingerprintAndInvalidSnapshot(t *testing.T) {
	now := time.Now()
	expires := now.Add(-time.Hour)
	assets := writeLicenseAssets(t, IssueOptions{
		ID:        "L-expired",
		Product:   "goframe",
		IssuedAt:  now.Add(-48 * time.Hour),
		ExpiresAt: &expires,
	})

	withLicenseConfig(t, Config{
		Enabled:            true,
		File:               assets.licensePath,
		PublicKey:          assets.publicPath,
		Strict:             false,
		FingerprintSources: []string{"hostid", "mac"},
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	manager := Get()
	if manager == nil {
		t.Fatal("expected manager even when license is expired")
	}
	if manager.Status() != StatusExpired {
		t.Fatalf("status = %s", manager.Status())
	}
	if manager.Snapshot().Reason == "" {
		t.Fatal("expected reason for expired license")
	}
}

func TestBootFingerprintUnavailable(t *testing.T) {
	now := time.Now()
	assets := writeLicenseAssets(t, IssueOptions{ID: "L-nofp", Product: "p", IssuedAt: now})

	withLicenseConfig(t, Config{
		Enabled:            true,
		File:               assets.licensePath,
		PublicKey:          assets.publicPath,
		Fingerprint:        "",
		FingerprintSources: []string{"unknown"},
	})
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if manager := Get(); manager == nil || !manager.Valid() {
		t.Fatalf("manager = %+v", manager)
	}
}

func TestLoadPublicKeyAndRevocation(t *testing.T) {
	publicPEM, privatePEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(publicPath, publicPEM, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	if _, err := loadPublicKey("   "); err == nil {
		t.Fatal("expected empty public key error")
	}
	if _, err := loadPublicKey(publicPath); err != nil {
		t.Fatalf("loadPublicKey file: %v", err)
	}
	if _, err := loadPublicKey(string(publicPEM)); err != nil {
		t.Fatalf("loadPublicKey inline: %v", err)
	}

	privateKey, err := ParsePrivateKeyPEM(privatePEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		t.Fatal("decode public pem")
	}
	publicKey, err := ParsePublicKeyPEM(publicPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}

	list, err := loadRevocation("", publicKey)
	if err != nil || list != nil {
		t.Fatalf("empty revocation = %v, %v", list, err)
	}

	content, err := SignRevocation(&RevocationList{IDs: []string{"L-1"}}, privateKey)
	if err != nil {
		t.Fatalf("SignRevocation: %v", err)
	}
	revocationPath := filepath.Join(dir, "revocation.json")
	if err := os.WriteFile(revocationPath, content, 0o600); err != nil {
		t.Fatalf("write revocation: %v", err)
	}
	list, err = loadRevocation(revocationPath, publicKey)
	if err != nil || !list.Contains("L-1") {
		t.Fatalf("loadRevocation = %v, %v", list, err)
	}
}

func TestHTTPRegisterAndRequireValid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mount := func() *gin.Engine {
		engine := gin.New()
		if err := api.MountRoot(engine); err != nil {
			t.Fatalf("mount root: %v", err)
		}
		return engine
	}

	// 未启用或路径为空时不登记路由。
	withLicenseConfig(t, Config{Enabled: false, StatusPath: "/license"})
	api.Reset()
	Register()
	engine := mount()
	if code := getStatus(engine, "/license"); code != http.StatusNotFound {
		t.Fatalf("disabled status = %d", code)
	}

	withLicenseConfig(t, Config{Enabled: true, StatusPath: "  "})
	api.Reset()
	Register()
	engine = mount()
	if code := getStatus(engine, "/license"); code != http.StatusNotFound {
		t.Fatalf("empty path status = %d", code)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	manager := newTestManager(t, now, IssueOptions{ID: "L-http", Product: "goframe", IssuedAt: now}, nil)
	global.manager = manager
	t.Cleanup(func() {
		global.manager = nil
	})

	withLicenseConfig(t, Config{Enabled: true, StatusPath: "license/"})
	api.Reset()
	Register()
	engine = mount()
	if code := getStatus(engine, "/license"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	engine = gin.New()
	engine.GET("/valid", RequireValid(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	if code := getStatus(engine, "/valid"); code != http.StatusOK {
		t.Fatalf("valid status = %d", code)
	}

	expires := now.Add(-2 * time.Hour)
	expired := newTestManager(t, now, IssueOptions{
		ID: "L-expired", Product: "goframe", IssuedAt: now.Add(-48 * time.Hour), ExpiresAt: &expires,
	}, nil)
	global.manager = expired
	if code := getStatus(engine, "/valid"); code != http.StatusForbidden {
		t.Fatalf("expired status = %d", code)
	}

	global.manager = nil
	if code := getStatus(engine, "/valid"); code != http.StatusServiceUnavailable {
		t.Fatalf("missing manager status = %d", code)
	}
}

func TestResolvePath(t *testing.T) {
	cases := map[string]string{
		"":          DefaultConfig().StatusPath,
		"   ":       DefaultConfig().StatusPath,
		"license":   "/license",
		"/license/": "/license",
		"///":       DefaultConfig().StatusPath,
		"/a/b/c/":   "/a/b/c",
	}
	for input, want := range cases {
		if got := resolvePath(input); got != want {
			t.Fatalf("resolvePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }
