package web

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/god-jason/goframe/logger"
)

type fakeDNSProvider struct {
	present int
	cleanup int
}

func (f *fakeDNSProvider) Present(context.Context, string, string, string) error {
	f.present++
	return nil
}

func (f *fakeDNSProvider) CleanUp(context.Context, string, string, string) error {
	f.cleanup++
	return nil
}

// stubACMEHooks 用假实现替换真实 ACME 网络调用，并在测试结束后恢复。
func stubACMEHooks(t *testing.T, register func() (*registration.Resource, error), obtain func() (*certificate.Resource, error)) {
	t.Helper()
	originalRegister := registerACMEAccount
	originalObtain := obtainACMECertificate
	registerACMEAccount = func(*lego.Client, *legoUser) (*registration.Resource, error) { return register() }
	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return obtain()
	}
	t.Cleanup(func() {
		registerACMEAccount = originalRegister
		obtainACMECertificate = originalObtain
	})
}

// stubLegoClient 避免 lego.NewClient 访问真实 ACME 目录。
func stubLegoClient(t *testing.T) {
	t.Helper()
	originalNewClient := newLegoClient
	originalSetProvider := setACMEDNSProvider
	newLegoClient = func(*lego.Config) (*lego.Client, error) { return &lego.Client{}, nil }
	setACMEDNSProvider = func(*lego.Client, DNSProvider) error { return nil }
	t.Cleanup(func() {
		newLegoClient = originalNewClient
		setACMEDNSProvider = originalSetProvider
	})
}

// TestACMEDNSAdapter 覆盖 lego 适配器与用户实现。
func TestACMEDNSAdapter(t *testing.T) {
	provider := &fakeDNSProvider{}
	adapter := &legoDNSAdapter{provider: provider}

	if err := adapter.Present("example.com", "token", "keyauth"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if err := adapter.CleanUp("example.com", "token", "keyauth"); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	if provider.present != 1 || provider.cleanup != 1 {
		t.Fatalf("provider calls = %d/%d", provider.present, provider.cleanup)
	}

	timeout, interval := adapter.Timeout()
	if timeout != 2*time.Minute || interval != 10*time.Second {
		t.Fatalf("timeout = %v, interval = %v", timeout, interval)
	}

	user := &legoUser{email: "admin@example.com", key: "key", registration: &registration.Resource{}}
	if user.GetEmail() != "admin@example.com" {
		t.Fatalf("email = %q", user.GetEmail())
	}
	if user.GetRegistration() == nil {
		t.Fatal("registration must not be nil")
	}
	if user.GetPrivateKey() != "key" {
		t.Fatalf("private key = %v", user.GetPrivateKey())
	}
}

func TestLoadOrCreateAccountKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.key")

	first, err := loadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if first == nil {
		t.Fatal("generated key is nil")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("account key file missing: %v", err)
	}

	second, err := loadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	if second == nil {
		t.Fatal("loaded key is nil")
	}

	invalid := filepath.Join(t.TempDir(), "invalid.key")
	if err := os.WriteFile(invalid, []byte("not a pem key"), 0o600); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}
	if _, err := loadOrCreateAccountKey(invalid); err == nil {
		t.Fatal("expected invalid key parse error")
	}

	missingDir := filepath.Join(t.TempDir(), "missing", "account.key")
	if _, err := loadOrCreateAccountKey(missingDir); err == nil {
		t.Fatal("expected write error for missing directory")
	}
}

func writeCachedCertificate(t *testing.T, dir string, validFor time.Duration) {
	t.Helper()
	certPEM, keyPEM := selfSignedPEM(t, validFor)
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatalf("write cert cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0o600); err != nil {
		t.Fatalf("write key cache: %v", err)
	}
}

func TestACMEDNSManagerLoadAndCertificate(t *testing.T) {
	dir := t.TempDir()
	manager := &acmeDNSManager{dir: dir, renewBefore: time.Hour, log: logger.NewNop()}

	if !manager.needsRenewal() {
		t.Fatal("empty manager must need renewal")
	}
	if err := manager.load(); err == nil {
		t.Fatal("expected missing cache error")
	}

	writeCachedCertificate(t, dir, 365*24*time.Hour)
	if err := manager.load(); err != nil {
		t.Fatalf("load cached certificate: %v", err)
	}
	if manager.needsRenewal() {
		t.Fatal("fresh certificate must not need renewal")
	}
	cert, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("certificate = %+v", cert)
	}
}

func TestACMEDNSManagerGetCertificateRenews(t *testing.T) {
	dir := t.TempDir()
	writeCachedCertificate(t, dir, -time.Hour)
	manager := &acmeDNSManager{dir: dir, renewBefore: time.Hour, log: logger.NewNop()}
	if err := manager.load(); err != nil {
		t.Fatalf("load expired certificate: %v", err)
	}
	if !manager.needsRenewal() {
		t.Fatal("expired certificate must need renewal")
	}

	certPEM, keyPEM := selfSignedPEM(t, 365*24*time.Hour)
	stubACMEHooks(t,
		func() (*registration.Resource, error) { return &registration.Resource{}, nil },
		func() (*certificate.Resource, error) {
			return &certificate.Resource{Certificate: certPEM, PrivateKey: keyPEM}, nil
		},
	)

	cert, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil || manager.needsRenewal() {
		t.Fatalf("renewed certificate invalid: %+v", cert)
	}

	// 续期失败时返回错误，不返回过期证书。
	writeCachedCertificate(t, dir, -time.Hour)
	if err := manager.load(); err != nil {
		t.Fatalf("reload expired certificate: %v", err)
	}
	registerACMEAccount = func(*lego.Client, *legoUser) (*registration.Resource, error) {
		return nil, errors.New("register failed")
	}
	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return nil, errors.New("obtain failed")
	}
	if _, err := manager.GetCertificate(nil); err == nil || !strings.Contains(err.Error(), "obtain") {
		t.Fatalf("err = %v", err)
	}
}

func TestACMEDNSManagerObtainErrors(t *testing.T) {
	baseDir := t.TempDir()
	manager := &acmeDNSManager{dir: baseDir, log: logger.NewNop(), domains: []string{"example.com"}}

	originalObtain := obtainACMECertificate
	t.Cleanup(func() { obtainACMECertificate = originalObtain })

	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return nil, errors.New("network error")
	}
	if err := manager.obtain(context.Background()); err == nil {
		t.Fatal("expected obtain error")
	}

	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return &certificate.Resource{Certificate: []byte("bad"), PrivateKey: []byte("bad")}, nil
	}
	if err := manager.obtain(context.Background()); err == nil {
		t.Fatal("expected keypair parse error")
	}

	certPEM, keyPEM := selfSignedPEM(t, time.Hour)
	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return &certificate.Resource{Certificate: certPEM, PrivateKey: keyPEM}, nil
	}

	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	manager.dir = blocker
	if err := manager.obtain(context.Background()); err == nil {
		t.Fatal("expected certificate write error")
	}

	manager.dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(manager.dir, "key.pem"), 0o700); err != nil {
		t.Fatalf("create blocking key dir: %v", err)
	}
	if err := manager.obtain(context.Background()); err == nil {
		t.Fatal("expected key write error")
	}

	manager.dir = t.TempDir()
	if err := manager.obtain(context.Background()); err != nil {
		t.Fatalf("obtain: %v", err)
	}
	if manager.certificate == nil || manager.leaf == nil {
		t.Fatalf("manager not populated: %+v", manager)
	}
	if _, err := os.Stat(filepath.Join(manager.dir, "cert.pem")); err != nil {
		t.Fatalf("certificate cache missing: %v", err)
	}
}

func TestNewACMEDNSManagerErrors(t *testing.T) {
	stubLegoClient(t)
	originalProvider := currentDNSProvider()
	t.Cleanup(func() { SetACMEDNSProvider(originalProvider) })
	SetACMEDNSProvider(&fakeDNSProvider{})

	cfg := ACMEConfig{
		Email:    "admin@example.com",
		Domains:  []string{"example.com"},
		CacheDir: t.TempDir(),
	}

	blocker := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	badCfg := cfg
	badCfg.CacheDir = blocker
	if _, err := newACMEDNSManager(badCfg, logger.NewNop()); err == nil {
		t.Fatal("expected cache dir error")
	}

	badKeyDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badKeyDir, "dns"), 0o700); err != nil {
		t.Fatalf("mkdir dns cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badKeyDir, "dns", "account.key"), []byte("invalid"), 0o600); err != nil {
		t.Fatalf("write invalid account key: %v", err)
	}
	badKeyCfg := cfg
	badKeyCfg.CacheDir = badKeyDir
	if _, err := newACMEDNSManager(badKeyCfg, logger.NewNop()); err == nil {
		t.Fatal("expected account key parse error")
	}

	stubACMEHooks(t,
		func() (*registration.Resource, error) { return nil, errors.New("register failed") },
		func() (*certificate.Resource, error) { return nil, errors.New("should not be called") },
	)
	if _, err := newACMEDNSManager(cfg, logger.NewNop()); err == nil || !strings.Contains(err.Error(), "register") {
		t.Fatalf("err = %v", err)
	}

	registerACMEAccount = func(*lego.Client, *legoUser) (*registration.Resource, error) {
		return &registration.Resource{}, nil
	}
	obtainACMECertificate = func(*lego.Client, certificate.ObtainRequest) (*certificate.Resource, error) {
		return nil, errors.New("obtain failed")
	}
	if _, err := newACMEDNSManager(cfg, logger.NewNop()); err == nil || !strings.Contains(err.Error(), "obtain") {
		t.Fatalf("err = %v", err)
	}

	newLegoClient = func(*lego.Config) (*lego.Client, error) {
		return nil, errors.New("client failed")
	}
	if _, err := newACMEDNSManager(cfg, logger.NewNop()); err == nil || !strings.Contains(err.Error(), "create acme client") {
		t.Fatalf("err = %v", err)
	}

	newLegoClient = func(*lego.Config) (*lego.Client, error) { return &lego.Client{}, nil }
	setACMEDNSProvider = func(*lego.Client, DNSProvider) error { return errors.New("provider failed") }
	if _, err := newACMEDNSManager(cfg, logger.NewNop()); err == nil || !strings.Contains(err.Error(), "set acme dns provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewACMEDNSManagerSuccessAndCache(t *testing.T) {
	stubLegoClient(t)
	originalProvider := currentDNSProvider()
	t.Cleanup(func() { SetACMEDNSProvider(originalProvider) })
	SetACMEDNSProvider(&fakeDNSProvider{})

	certPEM, keyPEM := selfSignedPEM(t, 365*24*time.Hour)
	obtainCalls := 0
	stubACMEHooks(t,
		func() (*registration.Resource, error) { return &registration.Resource{}, nil },
		func() (*certificate.Resource, error) {
			obtainCalls++
			return &certificate.Resource{Certificate: certPEM, PrivateKey: keyPEM}, nil
		},
	)

	cfg := ACMEConfig{
		Email:        "admin@example.com",
		Domains:      []string{"example.com"},
		CacheDir:     t.TempDir(),
		RenewBefore:  time.Hour,
		DirectoryURL: "https://acme.example.com/directory",
	}
	manager, err := newACMEDNSManager(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("newACMEDNSManager: %v", err)
	}
	if manager.certificate == nil || obtainCalls != 1 {
		t.Fatalf("certificate = %+v, obtain calls = %d", manager.certificate, obtainCalls)
	}

	// 第二次从缓存加载，不再申请。
	cached, err := newACMEDNSManager(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("newACMEDNSManager cached: %v", err)
	}
	if cached.certificate == nil || obtainCalls != 1 {
		t.Fatalf("cached certificate = %+v, obtain calls = %d", cached.certificate, obtainCalls)
	}

	// buildTLSConfig 的 dns-01 分支也能使用注入的假客户端。
	tlsCfg := TLSConfig{Mode: "acme", ACME: cfg}
	tlsCfg.ACME.Challenge = "dns-01"
	material, err := buildTLSConfig(tlsCfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig dns-01: %v", err)
	}
	if material.config == nil || material.config.GetCertificate == nil {
		t.Fatalf("unexpected material: %+v", material)
	}
	cert, err := material.config.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate = %+v, %v", cert, err)
	}

	// 默认缓存目录与默认续期窗口。
	t.Chdir(t.TempDir())
	defaultCfg := ACMEConfig{Email: "admin@example.com", Domains: []string{"example.com"}}
	defaultManager, err := newACMEDNSManager(defaultCfg, logger.NewNop())
	if err != nil {
		t.Fatalf("newACMEDNSManager defaults: %v", err)
	}
	if defaultManager.certificate == nil || defaultManager.renewBefore != 30*24*time.Hour {
		t.Fatalf("unexpected manager: %+v", defaultManager)
	}
	if _, err := os.Stat(filepath.Join("data", "acme", "dns", "cert.pem")); err != nil {
		t.Fatalf("default cache certificate missing: %v", err)
	}
}
