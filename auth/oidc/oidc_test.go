package oidc

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func validOIDCConfig() Config {
	return Config{
		Issuer:       "https://issuer.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://app.example.com/callback",
	}
}

func TestConfigValidate(t *testing.T) {
	if err := validOIDCConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing issuer", mutate: func(c *Config) { c.Issuer = " " }},
		{name: "relative issuer", mutate: func(c *Config) { c.Issuer = "/issuer" }},
		{name: "unsupported issuer scheme", mutate: func(c *Config) { c.Issuer = "ftp://issuer.example.com" }},
		{name: "issuer with query", mutate: func(c *Config) { c.Issuer = "https://issuer.example.com?x=1" }},
		{name: "missing client id", mutate: func(c *Config) { c.ClientID = "" }},
		{name: "missing redirect", mutate: func(c *Config) { c.RedirectURL = "" }},
		{name: "relative redirect", mutate: func(c *Config) { c.RedirectURL = "/callback" }},
		{name: "unsupported redirect scheme", mutate: func(c *Config) { c.RedirectURL = "javascript:alert(1)" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validOIDCConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid config to be rejected")
			}
		})
	}
}

func TestAuthCodeURL(t *testing.T) {
	client, err := newClient(validOIDCConfig())
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	client.oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  "https://issuer.example.com/oauth2/authorize",
		TokenURL: "https://issuer.example.com/oauth2/token",
	}

	raw := client.AuthCodeURL("state-123")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "issuer.example.com" || parsed.Path != "/oauth2/authorize" {
		t.Fatalf("unexpected auth url: %s", raw)
	}

	query := parsed.Query()
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %s, want code", query.Get("response_type"))
	}
	if query.Get("client_id") != "client-id" {
		t.Fatalf("client_id = %s, want client-id", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "https://app.example.com/callback" {
		t.Fatalf("redirect_uri = %s", query.Get("redirect_uri"))
	}
	if query.Get("state") != "state-123" {
		t.Fatalf("state = %s, want state-123", query.Get("state"))
	}

	scopes := strings.Fields(query.Get("scope"))
	wantScopes := []string{"openid", "profile", "email"}
	if !reflect.DeepEqual(scopes, wantScopes) {
		t.Fatalf("scopes = %v, want %v", scopes, wantScopes)
	}
}

func TestScopeNormalization(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.Scopes = []string{" custom ", "openid", "custom", ""}
	client, err := newClient(cfg)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	want := []string{"openid", "custom"}
	if !reflect.DeepEqual(client.cfg.Scopes, want) {
		t.Fatalf("scopes = %v, want %v", client.cfg.Scopes, want)
	}

	cfg.Scopes = []string{"groups", "email"}
	client, err = newClient(cfg)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	want = []string{"openid", "groups", "email"}
	if !reflect.DeepEqual(client.cfg.Scopes, want) {
		t.Fatalf("scopes = %v, want %v", client.cfg.Scopes, want)
	}
}

func TestClientWithoutVerifier(t *testing.T) {
	client, err := newClient(validOIDCConfig())
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if _, _, err := client.Exchange(context.Background(), "code"); err == nil {
		t.Fatal("expected exchange without verifier to fail")
	}
	if _, err := client.VerifyIDToken(context.Background(), "token"); err == nil {
		t.Fatal("expected verify without verifier to fail")
	}
	if _, _, err := client.Exchange(context.Background(), ""); err == nil {
		t.Fatal("expected empty code to be rejected")
	}
}
