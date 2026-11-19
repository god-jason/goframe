package web

import (
	"net"
	"testing"
	"time"
)

func TestConfigValidateBranches(t *testing.T) {
	valid := DefaultConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		ok     bool
	}{
		{"port zero", func(c *Config) { c.Port = 0 }, false},
		{"port too large", func(c *Config) { c.Port = 70000 }, false},
		{"bad mode", func(c *Config) { c.Mode = "production" }, false},
		{"debug mode", func(c *Config) { c.Mode = "debug" }, true},
		{"test mode", func(c *Config) { c.Mode = "test" }, true},
		{"negative read timeout", func(c *Config) { c.ReadTimeout = -time.Second }, false},
		{"negative write timeout", func(c *Config) { c.WriteTimeout = -time.Second }, false},
		{"negative idle timeout", func(c *Config) { c.IdleTimeout = -time.Second }, false},
		{"zero shutdown timeout", func(c *Config) { c.ShutdownTimeout = 0 }, false},
		{"api prefix without slash", func(c *Config) { c.APIPrefix = "api" }, false},
		{"trusted ip", func(c *Config) { c.TrustedProxies = []string{"127.0.0.1"} }, true},
		{"trusted cidr", func(c *Config) { c.TrustedProxies = []string{"10.0.0.0/8"} }, true},
		{"invalid trusted proxy", func(c *Config) { c.TrustedProxies = []string{"not-an-ip"} }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if cfg := DefaultConfig(); cfg.Addr() != net.JoinHostPort("0.0.0.0", "8080") {
		t.Fatalf("addr = %q", cfg.Addr())
	}
}

func TestTLSValidateBranches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TLSConfig)
		ok     bool
	}{
		{"none", func(c *TLSConfig) { c.Mode = "none" }, true},
		{"file missing", func(c *TLSConfig) { c.Mode = "file" }, false},
		{"file present", func(c *TLSConfig) { c.Mode = "file"; c.CertFile = "a.pem"; c.KeyFile = "b.pem" }, true},
		{"acme missing email", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Domains = []string{"example.com"}
		}, false},
		{"acme missing domains", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
		}, false},
		{"acme default challenge", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
			c.ACME.Domains = []string{"example.com"}
		}, true},
		{"acme tls-alpn", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
			c.ACME.Domains = []string{"example.com"}
			c.ACME.Challenge = "tls-alpn-01"
		}, true},
		{"acme dns", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
			c.ACME.Domains = []string{"example.com"}
			c.ACME.Challenge = "dns-01"
		}, true},
		{"acme bad challenge", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
			c.ACME.Domains = []string{"example.com"}
			c.ACME.Challenge = "ftp-01"
		}, false},
		{"acme http-01 disabled", func(c *TLSConfig) {
			c.Mode = "acme"
			c.ACME.Email = "admin@example.com"
			c.ACME.Domains = []string{"example.com"}
			c.ACME.Challenge = "http-01"
			c.ACME.DisableHTTP = true
		}, false},
		{"unsupported mode", func(c *TLSConfig) { c.Mode = "bogus" }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultTLSConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCurrentAndPProf(t *testing.T) {
	applyConfig(t, "")

	if cfg := GetConfig(); cfg == nil || cfg.Port == 0 {
		t.Fatalf("current config = %+v", cfg)
	}
	if cfg := CurrentTLSConfig(); cfg == nil || cfg.Mode != "none" {
		t.Fatalf("current tls config = %+v", cfg)
	}
	if cfg := PProf(); cfg == nil || cfg.Enabled || cfg.Path == "" {
		t.Fatalf("pprof config = %+v", cfg)
	}
	if cfg := DefaultPProfConfig(); cfg.Enabled || cfg.Path != "/debug/pprof" {
		t.Fatalf("default pprof = %+v", cfg)
	}
}
