package gormstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/god-jason/goframe/auth"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repository, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return repository
}

func testSession() *auth.Session {
	now := time.Now()
	return &auth.Session{
		ID:           "s1",
		UserID:       "u1",
		TenantID:     "t1",
		Roles:        []string{"admin"},
		Permissions:  []string{"user:read"},
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(time.Hour),
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	session := testSession()

	if err := repository.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := repository.CreateSession(ctx, session); err == nil {
		t.Fatal("expected duplicate error")
	}

	got, err := repository.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != "u1" || len(got.Roles) != 1 || got.Roles[0] != "admin" {
		t.Fatalf("unexpected session: %+v", got)
	}

	got.LastActiveAt = time.Now()
	if err := repository.UpdateSession(ctx, got); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	sessions, err := repository.ListSessions(ctx, "u1")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %d, %v", len(sessions), err)
	}

	if err := repository.RevokeSession(ctx, session.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	got, _ = repository.GetSession(ctx, session.ID)
	if got.RevokedAt == nil {
		t.Fatal("session not revoked")
	}
	if err := repository.RevokeSession(ctx, "missing", time.Now()); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestRefreshTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	now := time.Now()

	token := &auth.RefreshToken{
		ID:        "r1",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-1",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := repository.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := repository.CreateRefreshToken(ctx, token); err == nil {
		t.Fatal("expected duplicate error")
	}

	got, err := repository.GetRefreshToken(ctx, "hash-1")
	if err != nil || got.ID != "r1" {
		t.Fatalf("GetRefreshToken = %+v, %v", got, err)
	}

	next := &auth.RefreshToken{
		ID:            "r2",
		SessionID:     "s1",
		FamilyID:      "f1",
		TokenHash:     "hash-2",
		ParentTokenID: "r1",
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}
	if err := repository.RotateRefreshToken(ctx, "r1", next, now); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	old, _ := repository.GetRefreshToken(ctx, "hash-1")
	if old.RotatedAt == nil {
		t.Fatal("old token not marked rotated")
	}
	if err := repository.RotateRefreshToken(ctx, "r1", next, now); err == nil {
		t.Fatal("expected rotation of used token to fail")
	}

	if err := repository.RevokeTokenFamily(ctx, "f1", now); err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}
	rotated, _ := repository.GetRefreshToken(ctx, "hash-2")
	if rotated.RevokedAt == nil {
		t.Fatal("token family not revoked")
	}
	if err := repository.RevokeSessionTokens(ctx, "s1", now); err != nil {
		t.Fatalf("RevokeSessionTokens: %v", err)
	}
}
