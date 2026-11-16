package auth

import (
	"context"
	"testing"
	"time"
)

func TestMemorySessionRepositoryLifecycle(t *testing.T) {
	repo := NewMemorySessionRepository()
	ctx := context.Background()
	now := time.Now().UTC()

	older := &Session{
		ID:        "s1",
		UserID:    "u1",
		CreatedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	}
	newer := &Session{
		ID:        "s2",
		UserID:    "u1",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	for _, session := range []*Session{older, newer} {
		if err := repo.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	if err := repo.CreateSession(ctx, older); err == nil {
		t.Fatal("expected duplicate session to be rejected")
	}

	got, err := repo.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("user id = %q", got.UserID)
	}
	if _, err := repo.GetSession(ctx, "missing"); err == nil {
		t.Fatal("expected missing session error")
	}

	list, err := repo.ListSessions(ctx, "u1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 || list[0].ID != "s2" {
		t.Fatalf("expected newest session first, got %+v", list)
	}

	got.LastActiveAt = now
	if err := repo.UpdateSession(ctx, got); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := repo.UpdateSession(ctx, &Session{ID: "missing"}); err == nil {
		t.Fatal("expected update missing session error")
	}
}

func TestMemorySessionRepositoryRevocation(t *testing.T) {
	repo := NewMemorySessionRepository()
	ctx := context.Background()
	now := time.Now().UTC()

	session := &Session{ID: "s1", UserID: "u1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := repo.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := repo.RevokeSession(ctx, "s1", now); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	got, _ := repo.GetSession(ctx, "s1")
	if got.RevokedAt == nil {
		t.Fatal("expected session to be revoked")
	}
	if got.Active(now) {
		t.Fatal("revoked session must not be active")
	}
	if err := repo.RevokeSession(ctx, "missing", now); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestMemoryRefreshTokenRotation(t *testing.T) {
	repo := NewMemorySessionRepository()
	ctx := context.Background()
	now := time.Now().UTC()

	first := &RefreshToken{
		ID:        "r1",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-1",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := repo.CreateRefreshToken(ctx, first); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := repo.CreateRefreshToken(ctx, first); err == nil {
		t.Fatal("expected duplicate token hash to be rejected")
	}

	second := &RefreshToken{
		ID:            "r2",
		SessionID:     "s1",
		FamilyID:      "f1",
		TokenHash:     "hash-2",
		ParentTokenID: "r1",
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}
	if err := repo.RotateRefreshToken(ctx, "r1", second, now); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	if err := repo.RotateRefreshToken(ctx, "r1", second, now); err == nil {
		t.Fatal("expected rotated token to be rejected on second rotation")
	}

	stored, err := repo.GetRefreshToken(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	if stored.RotatedAt == nil {
		t.Fatal("expected old token to be marked rotated")
	}

	if err := repo.RevokeTokenFamily(ctx, "f1", now); err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}
	secondStored, _ := repo.GetRefreshToken(ctx, "hash-2")
	if secondStored.RevokedAt == nil {
		t.Fatal("expected family tokens to be revoked")
	}
}

func TestMemoryRefreshTokenSessionRevocation(t *testing.T) {
	repo := NewMemorySessionRepository()
	ctx := context.Background()
	now := time.Now().UTC()

	token := &RefreshToken{
		ID:        "r1",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-1",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	_ = repo.CreateRefreshToken(ctx, token)

	if err := repo.RevokeSessionTokens(ctx, "s1", now); err != nil {
		t.Fatalf("RevokeSessionTokens: %v", err)
	}
	stored, _ := repo.GetRefreshToken(ctx, "hash-1")
	if stored.RevokedAt == nil {
		t.Fatal("expected token to be revoked")
	}

	if _, err := repo.GetRefreshToken(ctx, "missing"); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestRevokeUserSessionsExceptCurrent(t *testing.T) {
	repo := NewMemorySessionRepository()
	ctx := context.Background()
	now := time.Now().UTC()

	for _, id := range []string{"s1", "s2", "s3"} {
		_ = repo.CreateSession(ctx, &Session{ID: id, UserID: "u1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
		_ = repo.CreateRefreshToken(ctx, &RefreshToken{
			ID:        "r-" + id,
			SessionID: id,
			FamilyID:  "f-" + id,
			TokenHash: "hash-" + id,
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
		})
	}

	if err := repo.RevokeUserSessions(ctx, "u1", "s2", now); err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}

	first, _ := repo.GetSession(ctx, "s1")
	second, _ := repo.GetSession(ctx, "s2")
	third, _ := repo.GetSession(ctx, "s3")
	if first.RevokedAt == nil || third.RevokedAt == nil {
		t.Fatal("expected other sessions to be revoked")
	}
	if second.RevokedAt != nil {
		t.Fatal("expected current session to stay active")
	}

	firstToken, _ := repo.GetRefreshToken(ctx, "hash-s1")
	if firstToken.RevokedAt == nil {
		t.Fatal("expected revoked session tokens to be revoked")
	}
}

func TestSessionActive(t *testing.T) {
	now := time.Now().UTC()
	active := &Session{ExpiresAt: now.Add(time.Minute)}
	if !active.Active(now) {
		t.Fatal("expected session to be active")
	}

	revokedAt := now.Add(-time.Minute)
	revoked := &Session{ExpiresAt: now.Add(time.Minute), RevokedAt: &revokedAt}
	if revoked.Active(now) {
		t.Fatal("expected revoked session to be inactive")
	}

	expired := &Session{ExpiresAt: now.Add(-time.Minute)}
	if expired.Active(now) {
		t.Fatal("expected expired session to be inactive")
	}
}
