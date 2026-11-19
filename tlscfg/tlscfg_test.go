package tlscfg

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCertificate 生成一张自签名测试证书并写入 dir，返回证书与私钥文件路径。
func writeCertificate(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlscfg-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestServer(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertificate(t, dir)

	cfg, err := Server(certFile, keyFile)
	if err != nil {
		t.Fatalf("Server: %v", err)
	}
	if cfg.MinVersion != MinVersion || len(cfg.Certificates) != 1 {
		t.Fatalf("unexpected tls config: %+v", cfg)
	}

	if _, err := Server(filepath.Join(dir, "missing.pem"), keyFile); err == nil {
		t.Fatal("expected error for missing certificate")
	}
}

func TestClient(t *testing.T) {
	cfg, err := Client("", "", "", "example.com")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if cfg.ServerName != "example.com" || cfg.MinVersion != MinVersion {
		t.Fatalf("unexpected tls config: %+v", cfg)
	}

	dir := t.TempDir()
	if _, err := Client(filepath.Join(dir, "missing.pem"), "", "", ""); err == nil {
		t.Fatal("expected missing ca error")
	}
	invalidCA := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(invalidCA, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Client(invalidCA, "", "", ""); err == nil {
		t.Fatal("expected invalid ca error")
	}
	if _, err := Client("", "missing.crt", "missing.key", ""); err == nil {
		t.Fatal("expected missing client certificate error")
	}

	certFile, keyFile := writeCertificate(t, dir)
	cfg, err = Client(certFile, certFile, keyFile, "example.com")
	if err != nil {
		t.Fatalf("Client with files: %v", err)
	}
	if cfg.RootCAs == nil || len(cfg.Certificates) != 1 || cfg.ServerName != "example.com" {
		t.Fatalf("unexpected tls config: %+v", cfg)
	}
}
