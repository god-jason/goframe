package web

import (
	"strings"
	"testing"

	"github.com/god-jason/goframe/logger"
)

func TestBuildTLSConfigDNS01RequiresProvider(t *testing.T) {
	cfg := DefaultTLSConfig()
	cfg.Mode = "acme"
	cfg.ACME.Email = "admin@example.com"
	cfg.ACME.Domains = []string{"example.com"}
	cfg.ACME.Challenge = "dns-01"
	cfg.ACME.CacheDir = t.TempDir()

	_, err := buildTLSConfig(cfg, logger.NewNop())
	if err == nil || !strings.Contains(err.Error(), "SetACMEDNSProvider") {
		t.Fatalf("err = %v, want DNS provider requirement", err)
	}
}

func TestTLSValidateDNS01(t *testing.T) {
	cfg := DefaultTLSConfig()
	cfg.Mode = "acme"
	cfg.ACME.Email = "admin@example.com"
	cfg.ACME.Domains = []string{"example.com"}
	cfg.ACME.Challenge = "dns-01"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
