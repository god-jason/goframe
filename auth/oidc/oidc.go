// Package oidc 提供基于 OpenID Connect 的授权码登录与 ID Token 校验能力。
package oidc

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config 是 OIDC 客户端配置。
type Config struct {
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
}

// Validate 校验 OIDC 配置。
func (c Config) Validate() error {
	issuer := strings.TrimSpace(c.Issuer)
	if issuer == "" {
		return fmt.Errorf("oidc: issuer is required")
	}
	issuerURL, err := url.Parse(issuer)
	if err != nil || !issuerURL.IsAbs() || issuerURL.Host == "" {
		return fmt.Errorf("oidc: issuer must be an absolute URL")
	}
	if issuerURL.Scheme != "https" && issuerURL.Scheme != "http" {
		return fmt.Errorf("oidc: issuer scheme must be http or https")
	}
	if issuerURL.RawQuery != "" || issuerURL.Fragment != "" {
		return fmt.Errorf("oidc: issuer must not contain query or fragment")
	}

	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("oidc: client id is required")
	}
	if strings.TrimSpace(c.RedirectURL) == "" {
		return fmt.Errorf("oidc: redirect url is required")
	}
	redirectURL, err := url.Parse(strings.TrimSpace(c.RedirectURL))
	if err != nil || !redirectURL.IsAbs() || redirectURL.Host == "" {
		return fmt.Errorf("oidc: redirect url must be an absolute URL")
	}
	if redirectURL.Scheme != "https" && redirectURL.Scheme != "http" {
		return fmt.Errorf("oidc: redirect url scheme must be http or https")
	}
	return nil
}

// Claims 是业务关心的 ID Token 声明。
type Claims struct {
	Subject       string   `json:"sub"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Name          string   `json:"name"`
	Groups        []string `json:"groups"`
	TenantID      string   `json:"tenant_id"`
}

// Client 是 OIDC 授权码客户端。
type Client struct {
	cfg          Config
	oauth2Config oauth2.Config
	verifier     *coreoidc.IDTokenVerifier
}

// New 通过 issuer 发现 OIDC 元数据并创建客户端。
func New(ctx context.Context, cfg Config) (*Client, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}

	provider, err := coreoidc.NewProvider(ctx, client.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider: %w", err)
	}
	client.oauth2Config.Endpoint = provider.Endpoint()
	client.verifier = provider.Verifier(&coreoidc.Config{ClientID: client.cfg.ClientID})
	return client, nil
}

// AuthCodeURL 生成授权码登录地址。
func (c *Client) AuthCodeURL(state string) string {
	return c.oauth2Config.AuthCodeURL(state)
}

// Exchange 使用授权码换取令牌，返回原始 id_token 与校验后的声明。
func (c *Client) Exchange(ctx context.Context, code string) (string, *Claims, error) {
	if strings.TrimSpace(code) == "" {
		return "", nil, fmt.Errorf("oidc: authorization code is required")
	}
	if c.verifier == nil {
		return "", nil, fmt.Errorf("oidc: client is not initialized")
	}

	token, err := c.oauth2Config.Exchange(ctx, code)
	if err != nil {
		return "", nil, fmt.Errorf("oidc: exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", nil, fmt.Errorf("oidc: id token missing from token response")
	}

	claims, err := c.VerifyIDToken(ctx, rawIDToken)
	if err != nil {
		return rawIDToken, nil, err
	}
	return rawIDToken, claims, nil
}

// VerifyIDToken 校验原始 ID Token 并解析声明。
func (c *Client) VerifyIDToken(ctx context.Context, rawIDToken string) (*Claims, error) {
	if c.verifier == nil {
		return nil, fmt.Errorf("oidc: client is not initialized")
	}
	if strings.TrimSpace(rawIDToken) == "" {
		return nil, fmt.Errorf("oidc: id token is required")
	}

	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id token: %w", err)
	}
	claims := &Claims{}
	if err := idToken.Claims(claims); err != nil {
		return nil, fmt.Errorf("oidc: decode id token claims: %w", err)
	}
	return claims, nil
}

func newClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.Scopes = normalizeScopes(cfg.Scopes)

	return &Client{
		cfg: cfg,
		oauth2Config: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       cfg.Scopes,
		},
	}, nil
}

func normalizeScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes)+1)
	seen := make(map[string]bool, len(scopes)+1)
	hasOpenID := false

	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		if scope == coreoidc.ScopeOpenID {
			hasOpenID = true
		}
		out = append(out, scope)
	}

	if len(out) == 0 {
		return []string{coreoidc.ScopeOpenID, "profile", "email"}
	}
	if !hasOpenID {
		return append([]string{coreoidc.ScopeOpenID}, out...)
	}

	// openid 始终放在首位，便于调用方断言授权范围。
	result := make([]string, 0, len(out))
	result = append(result, coreoidc.ScopeOpenID)
	for _, scope := range out {
		if scope != coreoidc.ScopeOpenID {
			result = append(result, scope)
		}
	}
	return result
}
