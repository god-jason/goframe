package oidc

import "testing"

// TestConfigValidateRedirectScheme 覆盖 redirect url 为绝对地址但协议非 http/https 的分支。
func TestConfigValidateRedirectScheme(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.RedirectURL = "ftp://app.example.com/callback"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-http scheme redirect to be rejected")
	}
}
