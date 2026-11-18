package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/god-jason/goframe/logger"
)

// selfSignedPEM 生成 127.0.0.1/localhost 的自签证书与私钥 PEM。
func selfSignedPEM(t *testing.T, validFor time.Duration) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// writeSelfSignedCertificate 生成 127.0.0.1/localhost 的自签证书与私钥文件。
func writeSelfSignedCertificate(t *testing.T, dir string, validFor time.Duration) (certFile, keyFile string) {
	t.Helper()
	certPEM, keyPEM := selfSignedPEM(t, validFor)

	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestBuildTLSConfigFileMode(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeSelfSignedCertificate(t, dir, time.Hour)

	material, err := buildTLSConfig(TLSConfig{Mode: "file", CertFile: certFile, KeyFile: keyFile}, nil)
	if err != nil {
		t.Fatalf("buildTLSConfig file: %v", err)
	}
	if material.config == nil || len(material.config.Certificates) != 1 {
		t.Fatalf("unexpected material: %+v", material)
	}
	if material.config.MinVersion != minTLSVersion {
		t.Fatalf("min version = %d", material.config.MinVersion)
	}
	if material.challenge != nil {
		t.Fatal("file mode must not create challenge server")
	}
}

func TestBuildTLSConfigEmptyMode(t *testing.T) {
	material, err := buildTLSConfig(TLSConfig{}, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig empty: %v", err)
	}
	if material.config != nil {
		t.Fatalf("empty mode should not create tls config: %+v", material)
	}
}

func TestBuildTLSConfigACMEErrors(t *testing.T) {
	base := TLSConfig{Mode: "acme"}

	if _, err := buildTLSConfig(base, logger.NewNop()); err == nil {
		t.Fatal("expected missing domains error")
	}
	base.ACME.Domains = []string{"example.com"}
	if _, err := buildTLSConfig(base, logger.NewNop()); err == nil {
		t.Fatal("expected missing email error")
	}
	base.ACME.Email = "admin@example.com"
	base.ACME.Challenge = "ftp-01"
	if _, err := buildTLSConfig(base, logger.NewNop()); err == nil {
		t.Fatal("expected unsupported challenge error")
	}

	base.ACME.Challenge = ""
	base.ACME.CacheDir = filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base.ACME.CacheDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write cache blocker: %v", err)
	}
	if _, err := buildTLSConfig(base, logger.NewNop()); err == nil {
		t.Fatal("expected cache dir error")
	}
}

func TestBuildTLSConfigACMEVariants(t *testing.T) {
	cfg := TLSConfig{
		Mode: "acme",
		ACME: ACMEConfig{
			Email:        "admin@example.com",
			Domains:      []string{"example.com"},
			CacheDir:     t.TempDir(),
			RenewBefore:  time.Hour,
			DirectoryURL: "https://acme.example.com/directory",
		},
	}

	material, err := buildTLSConfig(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig acme: %v", err)
	}
	if material.config == nil || material.challenge == nil || material.requireHTTP {
		t.Fatalf("unexpected material: %+v", material)
	}

	cfg.ACME.Challenge = "http-01"
	cfg.ACME.HTTPChallengeAddr = "127.0.0.1:0"
	http01, err := buildTLSConfig(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig http-01: %v", err)
	}
	if http01.challenge == nil || !http01.requireHTTP {
		t.Fatalf("http-01 material = %+v", http01)
	}

	cfg.ACME.DisableHTTP = true
	disabled, err := buildTLSConfig(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig disabled http: %v", err)
	}
	if disabled.challenge != nil {
		t.Fatal("disable_http_challenge must not start challenge server")
	}

	cfg.ACME.DisableHTTP = false
	cfg.ACME.Challenge = "tls-alpn-01"
	alpn, err := buildTLSConfig(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig tls-alpn: %v", err)
	}
	if alpn.challenge != nil {
		t.Fatal("tls-alpn must not start http challenge server")
	}
}

func TestBuildTLSConfigACMEDefaultCacheDir(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := TLSConfig{
		Mode: "acme",
		ACME: ACMEConfig{
			Email:   "admin@example.com",
			Domains: []string{"example.com"},
		},
	}
	material, err := buildTLSConfig(cfg, logger.NewNop())
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if material.challengeAddr != ":80" {
		t.Fatalf("challenge addr = %q", material.challengeAddr)
	}
	if _, err := os.Stat(filepath.Join("data", "acme")); err != nil {
		t.Fatalf("default cache dir not created: %v", err)
	}
}
