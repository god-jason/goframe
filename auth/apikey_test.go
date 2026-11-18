package auth

import (
	"context"
	"testing"
	"time"
)

func TestAPIKeyGenerateAndVerify(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryAPIKeyRepository()
	SetAPIKeyRepository(repository)
	t.Cleanup(func() { SetAPIKeyRepository(nil) })

	plain, key, err := NewAPIKey("ci", "u1", "t1", []string{"service"}, []string{"user:read"}, time.Hour)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if key.Hash == plain || key.Prefix == "" {
		t.Fatal("api key must not store plaintext")
	}
	if err := repository.Create(ctx, key); err != nil {
		t.Fatalf("Create: %v", err)
	}

	identity, err := VerifyAPIKey(ctx, plain)
	if err != nil {
		t.Fatalf("VerifyAPIKey: %v", err)
	}
	if identity.UserID != "u1" || identity.TenantID != "t1" || identity.AuthType != "api_key" {
		t.Fatalf("identity = %+v", identity)
	}
	if !identity.HasPermission("user:read") {
		t.Fatal("permission missing")
	}

	if _, err := VerifyAPIKey(ctx, plain[:len(plain)-1]+"x"); err == nil {
		t.Fatal("expected invalid api key error")
	}
	if _, err := VerifyAPIKey(ctx, "invalid"); err == nil {
		t.Fatal("expected malformed api key error")
	}
}

func TestAPIKeyExpiredAndRevoked(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryAPIKeyRepository()
	SetAPIKeyRepository(repository)
	t.Cleanup(func() { SetAPIKeyRepository(nil) })

	plain, key, err := NewAPIKey("ci", "u1", "", nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	expiredAt := time.Now().Add(-time.Minute)
	key.ExpiresAt = &expiredAt
	if err := repository.Create(ctx, key); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := VerifyAPIKey(ctx, plain); err == nil {
		t.Fatal("expected expired api key error")
	}

	revokedPlain, revokedKey, err := NewAPIKey("ci2", "u1", "", nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if err := repository.Create(ctx, revokedKey); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repository.Revoke(ctx, revokedKey.ID, time.Now()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := VerifyAPIKey(ctx, revokedPlain); err == nil {
		t.Fatal("expected revoked api key error")
	}
}

func TestAPIKeyRequiresRepository(t *testing.T) {
	SetAPIKeyRepository(nil)
	if _, err := VerifyAPIKey(context.Background(), APIKeyPrefix+"abcdefghijklmnop"); err == nil {
		t.Fatal("expected not configured error")
	}
}
