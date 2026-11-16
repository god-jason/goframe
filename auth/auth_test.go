package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/god-jason/goframe/logger"
)

func testAuthConfig() Config {
	cfg := DefaultConfig()
	cfg.Secret = strings.Repeat("k", 32)
	return cfg
}

func TestArgon2PasswordHasher(t *testing.T) {
	hasher, err := NewPasswordHasher(PasswordConfig{
		Algorithm:   "argon2id",
		Memory:      8192,
		Iterations:  1,
		Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}

	hash, err := hasher.Hash("s3cret-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if strings.Contains(hash, "s3cret-password") {
		t.Fatal("hash must not contain plaintext password")
	}

	ok, err := hasher.Verify("s3cret-password", hash)
	if err != nil || !ok {
		t.Fatalf("Verify valid password = %v, %v", ok, err)
	}
	ok, err = hasher.Verify("wrong-password", hash)
	if err != nil || ok {
		t.Fatalf("Verify wrong password = %v, %v", ok, err)
	}
	if _, err := hasher.Verify("x", "not-a-hash"); err == nil {
		t.Fatal("expected invalid hash format error")
	}
}

func TestJWTAccessToken(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, expiresAt, err := manager.IssueAccessToken(&Identity{
		UserID:      "42",
		TenantID:    "tenant-1",
		SessionID:   "session-1",
		Roles:       []string{"admin"},
		Permissions: []string{"user:read"},
	})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if token == "" || expiresAt.IsZero() {
		t.Fatal("expected non-empty token and expiration")
	}

	identity, err := manager.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if identity.UserID != "42" || identity.TenantID != "tenant-1" || identity.SessionID != "session-1" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if !identity.HasPermission("user:read") {
		t.Fatal("expected permission to be preserved")
	}

	if _, err := manager.ParseAccessToken(token + "tampered"); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
	if _, err := manager.ParseAccessToken(""); err == nil {
		t.Fatal("expected empty token to be rejected")
	}
}

func TestSessionLoginRefreshAndReplayDetection(t *testing.T) {
	service, err := NewService(testAuthConfig(), NewMemorySessionRepository(), logger.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	tokens, identity, err := service.Login(ctx, &Identity{
		UserID:      "u1",
		Roles:       []string{"admin"},
		Permissions: []string{"user:read"},
	}, SessionMeta{IP: "127.0.0.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if identity.SessionID == "" {
		t.Fatal("expected session id")
	}
	if _, err := service.Authenticate(ctx, tokens.AccessToken); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	rotated, _, err := service.Refresh(ctx, tokens.RefreshToken, SessionMeta{})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.RefreshToken == tokens.RefreshToken {
		t.Fatal("refresh token must rotate")
	}

	// 旧 Token 重放：必须拒绝并撤销整个 Token Family。
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, SessionMeta{}); err == nil {
		t.Fatal("expected replayed refresh token to be rejected")
	}
	if _, _, err := service.Refresh(ctx, rotated.RefreshToken, SessionMeta{}); err == nil {
		t.Fatal("expected token family to be revoked")
	}

	if err := service.Logout(ctx, identity.SessionID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.Authenticate(ctx, rotated.AccessToken); err == nil {
		t.Fatal("expected revoked session to be rejected")
	}
}

func TestRevokeAllSessionsExceptCurrent(t *testing.T) {
	service, err := NewService(testAuthConfig(), NewMemorySessionRepository(), logger.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	first, firstIdentity, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login first: %v", err)
	}
	second, secondIdentity, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login second: %v", err)
	}
	if firstIdentity.SessionID == secondIdentity.SessionID {
		t.Fatal("expected distinct sessions")
	}

	if err := service.RevokeAllSessions(ctx, "u1", secondIdentity.SessionID); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if _, err := service.Authenticate(ctx, first.AccessToken); err == nil {
		t.Fatal("expected first session to be revoked")
	}
	if _, err := service.Authenticate(ctx, second.AccessToken); err != nil {
		t.Fatalf("expected current session to stay active: %v", err)
	}
}
