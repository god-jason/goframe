package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testKeyID = "test-key"

type tokenMode string

const (
	modeSuccess   tokenMode = "success"
	modeError     tokenMode = "error"
	modeNoIDToken tokenMode = "no_id_token"
	modeBadJWKS   tokenMode = "bad_jwks"
)

type oidcFixture struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	mode   tokenMode
	claims jwt.MapClaims
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	fixture := &oidcFixture{key: key, mode: modeSuccess}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                fixture.server.URL,
			"authorization_endpoint":                fixture.server.URL + "/authorize",
			"token_endpoint":                        fixture.server.URL + "/token",
			"jwks_uri":                              fixture.server.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		if fixture.mode == modeBadJWKS {
			_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"test-key","n":"!!!","e":"AQAB"}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": testKeyID,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		switch fixture.mode {
		case modeError:
			http.Error(w, "invalid code", http.StatusBadRequest)
			return
		case modeNoIDToken:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
			return
		}
		idToken, err := fixture.sign(fixture.claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})

	fixture.server = httptest.NewServer(mux)
	fixture.claims = fixture.defaultClaims()
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *oidcFixture) defaultClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":            f.server.URL,
		"sub":            "user-1",
		"aud":            "client-id",
		"exp":            now.Add(time.Hour).Unix(),
		"iat":            now.Unix(),
		"email":          "alice@example.com",
		"email_verified": true,
		"name":           "Alice",
		"groups":         []string{"admins"},
		"tenant_id":      "tenant-1",
	}
}

func (f *oidcFixture) sign(claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID
	return token.SignedString(f.key)
}

func (f *oidcFixture) client(t *testing.T) *Client {
	t.Helper()
	client, err := New(context.Background(), Config{
		Issuer:       f.server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://app.example.com/callback",
		Scopes:       []string{"openid", "profile"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestDiscoveryAuthCodeURLAndExchange(t *testing.T) {
	fixture := newOIDCFixture(t)
	client := fixture.client(t)

	raw := client.AuthCodeURL("state-1")
	if !strings.Contains(raw, fixture.server.URL+"/authorize") {
		t.Fatalf("unexpected auth url: %s", raw)
	}
	if !strings.Contains(raw, "state=state-1") {
		t.Fatalf("missing state: %s", raw)
	}

	rawToken, claims, err := client.Exchange(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if rawToken == "" || claims.Subject != "user-1" || claims.Email != "alice@example.com" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if !claims.EmailVerified || claims.Name != "Alice" || claims.TenantID != "tenant-1" {
		t.Fatalf("claims not decoded: %+v", claims)
	}
	if len(claims.Groups) != 1 || claims.Groups[0] != "admins" {
		t.Fatalf("groups = %v", claims.Groups)
	}

	verified, err := client.VerifyIDToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if verified.Subject != claims.Subject {
		t.Fatalf("verified subject = %s", verified.Subject)
	}
}

func TestNewDiscoveryFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	url := server.URL
	server.Close()

	_, err := New(context.Background(), Config{
		Issuer:      url,
		ClientID:    "client-id",
		RedirectURL: "https://app.example.com/callback",
	})
	if err == nil {
		t.Fatal("expected discovery failure")
	}
	if !strings.Contains(err.Error(), "discover provider") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := New(context.Background(), Config{}); err == nil {
		t.Fatal("expected invalid config error")
	}
}

func TestExchangeErrorResponses(t *testing.T) {
	fixture := newOIDCFixture(t)
	client := fixture.client(t)

	fixture.mode = modeError
	if _, _, err := client.Exchange(context.Background(), "code"); err == nil {
		t.Fatal("expected token endpoint error")
	}

	fixture.mode = modeNoIDToken
	if _, _, err := client.Exchange(context.Background(), "code"); err == nil {
		t.Fatal("expected missing id token error")
	}
}

func TestVerifyIDTokenFailures(t *testing.T) {
	fixture := newOIDCFixture(t)
	client := fixture.client(t)
	ctx := context.Background()

	if _, err := client.VerifyIDToken(ctx, "  "); err == nil {
		t.Fatal("expected empty token error")
	}

	// 过期。
	expired := fixture.defaultClaims()
	expired["exp"] = time.Now().Add(-time.Hour).Unix()
	token, err := fixture.sign(expired)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := client.VerifyIDToken(ctx, token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}

	// issuer 不匹配。
	badIssuer := fixture.defaultClaims()
	badIssuer["iss"] = "https://other-issuer.example.com"
	token, err = fixture.sign(badIssuer)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := client.VerifyIDToken(ctx, token); err == nil {
		t.Fatal("expected issuer mismatch to be rejected")
	}

	// audience 不匹配。
	badAudience := fixture.defaultClaims()
	badAudience["aud"] = "other-client"
	token, err = fixture.sign(badAudience)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := client.VerifyIDToken(ctx, token); err == nil {
		t.Fatal("expected audience mismatch to be rejected")
	}

	// 声明类型不匹配导致 Claims 解码失败。
	badClaims := fixture.defaultClaims()
	badClaims["groups"] = 12345
	token, err = fixture.sign(badClaims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := client.VerifyIDToken(ctx, token); err == nil {
		t.Fatal("expected claims decode error")
	}
}

func TestExchangeReturnsRawTokenOnVerificationFailure(t *testing.T) {
	fixture := newOIDCFixture(t)
	client := fixture.client(t)
	fixture.claims["exp"] = time.Now().Add(-time.Hour).Unix()

	rawToken, claims, err := client.Exchange(context.Background(), "code")
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if rawToken == "" || claims != nil {
		t.Fatalf("expected raw token without claims, got %q %+v", rawToken, claims)
	}
}

func TestVerifyIDTokenWithBrokenJWKS(t *testing.T) {
	fixture := newOIDCFixture(t)
	client := fixture.client(t)
	fixture.mode = modeBadJWKS

	token, err := fixture.sign(fixture.defaultClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := client.VerifyIDToken(context.Background(), token); err == nil {
		t.Fatal("expected jwks error")
	}
}

func TestValidateIssuerFragment(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.Issuer = "https://issuer.example.com#fragment"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected issuer with fragment to be rejected")
	}
}
