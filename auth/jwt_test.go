package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	apperrors "github.com/god-jason/goframe/errors"
)

func TestJWTExpiredToken(t *testing.T) {
	cfg := testAuthConfig()
	cfg.AccessTokenTTL = 5 * time.Millisecond
	cfg.Leeway = 0

	manager, err := NewJWTManager(cfg)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	token, _, err := manager.IssueAccessToken(&Identity{UserID: "u1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if _, err := manager.ParseAccessToken(token); err == nil {
		t.Fatal("expected expired token to be rejected")
	} else if got := apperrors.From(err); got.Code != apperrors.CodeTokenExpired {
		t.Fatalf("code = %d, want %d", got.Code, apperrors.CodeTokenExpired)
	}
}

func TestJWTAudienceAndIssuerMismatch(t *testing.T) {
	cfg := testAuthConfig()
	manager, err := NewJWTManager(cfg)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	token, _, err := manager.IssueAccessToken(&Identity{UserID: "u1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	otherAudience := cfg
	otherAudience.Audience = "another-api"
	manager2, _ := NewJWTManager(otherAudience)
	if _, err := manager2.ParseAccessToken(token); err == nil {
		t.Fatal("expected audience mismatch to be rejected")
	}

	otherIssuer := cfg
	otherIssuer.Issuer = "another-issuer"
	manager3, _ := NewJWTManager(otherIssuer)
	if _, err := manager3.ParseAccessToken(token); err == nil {
		t.Fatal("expected issuer mismatch to be rejected")
	}
}

func TestJWTRejectsNoneAlgorithm(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "goframe",
			Audience:  jwt.ClaimStrings{"goframe-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		UserID: "u1",
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}
	if _, err := manager.ParseAccessToken(token); err == nil {
		t.Fatal("expected none algorithm to be rejected")
	}
}

func TestJWTRejectsTokenWithoutUserID(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "goframe",
			Audience:  jwt.ClaimStrings{"goframe-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(manager.secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := manager.ParseAccessToken(token); err == nil {
		t.Fatal("expected token without user id to be rejected")
	}
}

func TestJWTGeneratedSecret(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Secret = ""

	manager, err := NewJWTManager(cfg)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	if !manager.GeneratedSecret() {
		t.Fatal("expected generated secret flag")
	}

	token, _, err := manager.IssueAccessToken(&Identity{UserID: "u1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := manager.ParseAccessToken(token); err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
}

func TestJWTInvalidConfiguration(t *testing.T) {
	cfg := testAuthConfig()
	cfg.AccessTokenTTL = 0
	if _, err := NewJWTManager(cfg); err == nil {
		t.Fatal("expected zero ttl to be rejected")
	}
}
