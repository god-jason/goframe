package web

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/god-jason/goframe/logger"
)

// DNSProvider 是 DNS-01 挑战所需的 DNS 厂商适配器。
// 业务在 boot.Startup 前通过 SetACMEDNSProvider 注入，例如阿里云或 Cloudflare 实现。
type DNSProvider interface {
	Present(ctx context.Context, domain, token, keyAuth string) error
	CleanUp(ctx context.Context, domain, token, keyAuth string) error
}

var dns = struct {
	sync.RWMutex
	provider DNSProvider
}{}

// SetACMEDNSProvider 注入 DNS-01 提供者，必须在 boot.Startup 前调用。
func SetACMEDNSProvider(provider DNSProvider) {
	dns.Lock()
	dns.provider = provider
	dns.Unlock()
}

func currentDNSProvider() DNSProvider {
	dns.RLock()
	defer dns.RUnlock()
	return dns.provider
}

// legoDNSAdapter 把框架的 DNSProvider 适配为 lego 的 challenge.Provider。
type legoDNSAdapter struct {
	provider DNSProvider
}

func (a *legoDNSAdapter) Present(domain, token, keyAuth string) error {
	return a.provider.Present(context.Background(), domain, token, keyAuth)
}

func (a *legoDNSAdapter) CleanUp(domain, token, keyAuth string) error {
	return a.provider.CleanUp(context.Background(), domain, token, keyAuth)
}

// Timeout 告知 lego DNS 记录传播的等待时长与检查间隔。
func (a *legoDNSAdapter) Timeout() (timeout, interval time.Duration) {
	return 2 * time.Minute, 10 * time.Second
}

var _ challenge.ProviderTimeout = (*legoDNSAdapter)(nil)

// 以下钩子用于隔离真实 ACME 网络调用，便于测试错误与缓存路径。
var (
	newLegoClient = func(config *lego.Config) (*lego.Client, error) {
		return lego.NewClient(config)
	}
	setACMEDNSProvider = func(client *lego.Client, provider DNSProvider) error {
		return client.Challenge.SetDNS01Provider(&legoDNSAdapter{provider: provider})
	}
	registerACMEAccount = func(client *lego.Client, user *legoUser) (*registration.Resource, error) {
		return client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	obtainACMECertificate = func(client *lego.Client, request certificate.ObtainRequest) (*certificate.Resource, error) {
		return client.Certificate.Obtain(request)
	}
)

// legoUser 实现 lego 的 registration.User。
type legoUser struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (u *legoUser) GetEmail() string                        { return u.email }
func (u *legoUser) GetRegistration() *registration.Resource { return u.registration }
func (u *legoUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// acmeDNSManager 使用 Let's Encrypt + DNS-01 申请并缓存证书。
type acmeDNSManager struct {
	domains     []string
	renewBefore time.Duration
	dir         string
	client      *lego.Client
	log         logger.Logger

	mu          sync.Mutex
	certificate *tls.Certificate
	leaf        *x509.Certificate
}

// newACMEDNSManager 创建 DNS-01 证书管理器并加载或申请证书。
func newACMEDNSManager(cfg ACMEConfig, log logger.Logger) (*acmeDNSManager, error) {
	provider := currentDNSProvider()
	if provider == nil {
		return nil, fmt.Errorf("server: tls.acme.challenge dns-01 requires web.SetACMEDNSProvider")
	}

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = "data/acme"
	}
	dir := filepath.Join(cacheDir, "dns")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("server: create acme dns cache dir: %w", err)
	}

	key, err := loadOrCreateAccountKey(filepath.Join(dir, "account.key"))
	if err != nil {
		return nil, err
	}
	user := &legoUser{email: cfg.Email, key: key}

	legoConfig := lego.NewConfig(user)
	legoConfig.CADirURL = lego.LEDirectoryProduction
	if cfg.DirectoryURL != "" {
		legoConfig.CADirURL = cfg.DirectoryURL
	}
	legoConfig.Certificate.KeyType = certcrypto.EC256

	client, err := newLegoClient(legoConfig)
	if err != nil {
		return nil, fmt.Errorf("server: create acme client: %w", err)
	}
	if err := setACMEDNSProvider(client, provider); err != nil {
		return nil, fmt.Errorf("server: set acme dns provider: %w", err)
	}

	resource, err := registerACMEAccount(client, user)
	if err != nil {
		return nil, fmt.Errorf("server: register acme account: %w", err)
	}
	user.registration = resource

	renewBefore := cfg.RenewBefore
	if renewBefore <= 0 {
		renewBefore = 30 * 24 * time.Hour
	}
	manager := &acmeDNSManager{
		domains:     append([]string(nil), cfg.Domains...),
		renewBefore: renewBefore,
		dir:         dir,
		client:      client,
		log:         log,
	}
	if err := manager.load(); err != nil {
		log.Debug("acme dns certificate cache miss", logger.Error(err))
	}
	if manager.needsRenewal() {
		if err := manager.obtain(context.Background()); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

// GetCertificate 返回当前证书，临近过期时自动续期。
func (m *acmeDNSManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.needsRenewal() {
		if err := m.obtain(context.Background()); err != nil {
			return nil, err
		}
	}
	if m.certificate == nil {
		return nil, fmt.Errorf("server: acme certificate is not available")
	}
	return m.certificate, nil
}

func (m *acmeDNSManager) needsRenewal() bool {
	if m.certificate == nil || m.leaf == nil {
		return true
	}
	return time.Now().Add(m.renewBefore).After(m.leaf.NotAfter)
}

// obtain 向 Let's Encrypt 申请证书并写入缓存。
func (m *acmeDNSManager) obtain(ctx context.Context) error {
	m.log.Info("acme dns certificate obtaining", logger.Strings("domains", m.domains))
	resource, err := obtainACMECertificate(m.client, certificate.ObtainRequest{
		Domains: m.domains,
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("server: obtain acme certificate: %w", err)
	}

	certificate, err := tls.X509KeyPair(resource.Certificate, resource.PrivateKey)
	if err != nil {
		return fmt.Errorf("server: parse acme certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return fmt.Errorf("server: parse acme leaf: %w", err)
	}

	certPath := filepath.Join(m.dir, "cert.pem")
	keyPath := filepath.Join(m.dir, "key.pem")
	if err := os.WriteFile(certPath, resource.Certificate, 0o600); err != nil {
		return fmt.Errorf("server: write acme certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, resource.PrivateKey, 0o600); err != nil {
		return fmt.Errorf("server: write acme key: %w", err)
	}

	m.certificate = &certificate
	m.leaf = leaf
	_ = ctx
	m.log.Info("acme dns certificate obtained",
		logger.Time("not_after", leaf.NotAfter),
		logger.Strings("domains", m.domains),
	)
	return nil
}

// load 从缓存读取证书。
func (m *acmeDNSManager) load() error {
	certPath := filepath.Join(m.dir, "cert.pem")
	keyPath := filepath.Join(m.dir, "key.pem")
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return err
	}
	m.certificate = &certificate
	m.leaf = leaf
	return nil
}

// loadOrCreateAccountKey 读取或生成 ACME 账户私钥。
func loadOrCreateAccountKey(path string) (crypto.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		key, err := certcrypto.ParsePEMPrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("server: parse acme account key: %w", err)
		}
		return key, nil
	}

	key, err := certcrypto.GeneratePrivateKey(certcrypto.EC256)
	if err != nil {
		return nil, fmt.Errorf("server: generate acme account key: %w", err)
	}
	if err := os.WriteFile(path, certcrypto.PEMEncode(key), 0o600); err != nil {
		return nil, fmt.Errorf("server: write acme account key: %w", err)
	}
	return key, nil
}
