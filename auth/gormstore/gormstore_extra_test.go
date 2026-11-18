package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

func newClosedRepository(t *testing.T) *Repository {
	t.Helper()
	repository := newTestRepository(t)
	sqlDB, err := repository.db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
	return repository
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected nil database error")
	}

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "closed.db")), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close()

	if _, err := New(db); err == nil {
		t.Fatal("expected migration error")
	}
}

func TestArgumentValidation(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)

	if err := repository.CreateSession(ctx, nil); err == nil {
		t.Fatal("expected nil session error")
	}
	if err := repository.CreateSession(ctx, &auth.Session{}); err == nil {
		t.Fatal("expected empty session id error")
	}
	if err := repository.UpdateSession(ctx, nil); err == nil {
		t.Fatal("expected nil update session error")
	}
	if err := repository.CreateRefreshToken(ctx, nil); err == nil {
		t.Fatal("expected nil token error")
	}
	if err := repository.CreateRefreshToken(ctx, &auth.RefreshToken{ID: "x"}); err == nil {
		t.Fatal("expected empty token hash error")
	}
	if err := repository.RotateRefreshToken(ctx, "old", nil, time.Now()); err == nil {
		t.Fatal("expected nil next token error")
	}
}

func TestIsDuplicate(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "gorm duplicated key", err: gorm.ErrDuplicatedKey, want: true},
		{name: "sqlite unique", err: errors.New("UNIQUE constraint failed: auth_sessions.id"), want: true},
		{name: "mysql duplicate", err: errors.New("Error 1062: Duplicate entry 'x' for key 'PRIMARY'"), want: true},
		{name: "postgres duplicate", err: errors.New(`duplicate key value violates unique constraint`), want: true},
		{name: "other", err: errors.New("connection reset"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicate(tc.err); got != tc.want {
				t.Fatalf("isDuplicate(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestStringCodecs(t *testing.T) {
	if got := encodeStrings(nil); got != "[]" {
		t.Fatalf("encodeStrings(nil) = %q", got)
	}
	if got := encodeStrings([]string{"a", "b"}); got != `["a","b"]` {
		t.Fatalf("encodeStrings = %q", got)
	}
	if got := decodeStrings(""); got != nil {
		t.Fatalf("decodeStrings(empty) = %#v", got)
	}
	if got := decodeStrings("not-json"); got != nil {
		t.Fatalf("decodeStrings(invalid) = %#v", got)
	}
	got := decodeStrings(`["x","y"]`)
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("decodeStrings = %#v", got)
	}
}

func TestNotFoundBranches(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)

	if _, err := repository.GetSession(ctx, "missing"); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("GetSession = %v, want not found", err)
	}
	if _, err := repository.GetRefreshToken(ctx, "missing"); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("GetRefreshToken = %v, want not found", err)
	}
	if err := repository.UpdateSession(ctx, &auth.Session{ID: "missing"}); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("UpdateSession = %v, want not found", err)
	}
	if err := repository.RotateRefreshToken(ctx, "missing", &auth.RefreshToken{
		ID:        "next",
		TokenHash: "next-hash",
	}, time.Now()); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("RotateRefreshToken = %v, want not found", err)
	}
}

func TestDatabaseErrorBranches(t *testing.T) {
	ctx := context.Background()
	repository := newClosedRepository(t)
	now := time.Now()

	cases := []struct {
		name string
		call func() error
	}{
		{"CreateSession", func() error {
			return repository.CreateSession(ctx, &auth.Session{ID: "s1"})
		}},
		{"GetSession", func() error {
			_, err := repository.GetSession(ctx, "s1")
			return err
		}},
		{"UpdateSession", func() error {
			return repository.UpdateSession(ctx, &auth.Session{ID: "s1"})
		}},
		{"ListSessions", func() error {
			_, err := repository.ListSessions(ctx, "u1")
			return err
		}},
		{"RevokeSession", func() error {
			return repository.RevokeSession(ctx, "s1", now)
		}},
		{"RevokeUserSessions", func() error {
			return repository.RevokeUserSessions(ctx, "u1", "", now)
		}},
		{"CreateRefreshToken", func() error {
			return repository.CreateRefreshToken(ctx, &auth.RefreshToken{ID: "r1", TokenHash: "h1"})
		}},
		{"GetRefreshToken", func() error {
			_, err := repository.GetRefreshToken(ctx, "h1")
			return err
		}},
		{"RotateRefreshToken", func() error {
			return repository.RotateRefreshToken(ctx, "r1", &auth.RefreshToken{ID: "r2", TokenHash: "h2"}, now)
		}},
		{"RevokeTokenFamily", func() error {
			return repository.RevokeTokenFamily(ctx, "f1", now)
		}},
		{"RevokeSessionTokens", func() error {
			return repository.RevokeSessionTokens(ctx, "s1", now)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("expected database error")
			}
		})
	}
}

func TestRevokeUserSessions(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	now := time.Now()

	for _, id := range []string{"a", "b", "c"} {
		if err := repository.CreateSession(ctx, &auth.Session{
			ID:        id,
			UserID:    "user-1",
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	if err := repository.RevokeUserSessions(ctx, "user-1", "a", now); err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	kept, err := repository.GetSession(ctx, "a")
	if err != nil {
		t.Fatalf("GetSession(a): %v", err)
	}
	if kept.RevokedAt != nil {
		t.Fatal("excluded session must stay active")
	}
	for _, id := range []string{"b", "c"} {
		session, err := repository.GetSession(ctx, id)
		if err != nil {
			t.Fatalf("GetSession(%s): %v", id, err)
		}
		if session.RevokedAt == nil {
			t.Fatalf("%s not revoked", id)
		}
	}

	// 不排除任何会话的路径。
	if err := repository.RevokeUserSessions(ctx, "user-1", "", now); err != nil {
		t.Fatalf("RevokeUserSessions all: %v", err)
	}
	kept, _ = repository.GetSession(ctx, "a")
	if kept.RevokedAt == nil {
		t.Fatal("expected all sessions revoked")
	}
}

func TestEmptySessionCollections(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)

	session := &auth.Session{
		ID:        "empty",
		UserID:    "user-empty",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := repository.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := repository.GetSession(ctx, "empty")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Roles) != 0 || len(got.Permissions) != 0 {
		t.Fatalf("expected empty slices, got %#v %#v", got.Roles, got.Permissions)
	}

	sessions, err := repository.ListSessions(ctx, "nobody")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty list, got %d", len(sessions))
	}
}

func TestRotateDuplicateRollsBack(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	now := time.Now()

	if err := repository.CreateRefreshToken(ctx, &auth.RefreshToken{
		ID:        "r-old",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-old",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := repository.CreateRefreshToken(ctx, &auth.RefreshToken{
		ID:        "r-existing",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-existing",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create existing: %v", err)
	}

	err := repository.RotateRefreshToken(ctx, "r-old", &auth.RefreshToken{
		ID:        "r-next",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-existing",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}, now)
	if !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("RotateRefreshToken = %v, want conflict", err)
	}

	old, err := repository.GetRefreshToken(ctx, "hash-old")
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	if old.RotatedAt != nil {
		t.Fatal("failed rotation must roll back the old token update")
	}
}

func TestCreateTokenGenericFailure(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)

	if err := repository.db.Exec(`CREATE TRIGGER fail_token_insert BEFORE INSERT ON auth_refresh_tokens
		WHEN NEW.id = 'r-fail' BEGIN SELECT RAISE(ABORT, 'insert failed'); END`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := repository.CreateRefreshToken(ctx, &auth.RefreshToken{
		ID:        "r-fail",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-fail",
	}); err == nil {
		t.Fatal("expected insert failure")
	} else if apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("expected generic error, got conflict: %v", err)
	}
}

func TestRotateCreateTokenGenericFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	now := time.Now()

	if err := repository.CreateRefreshToken(ctx, &auth.RefreshToken{
		ID:        "r-live",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-live",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repository.db.Exec(`CREATE TRIGGER fail_rotated_token BEFORE INSERT ON auth_refresh_tokens
		WHEN NEW.id = 'r-fail' BEGIN SELECT RAISE(ABORT, 'insert failed'); END`).Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	err := repository.RotateRefreshToken(ctx, "r-live", &auth.RefreshToken{
		ID:        "r-fail",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash-new",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}, now)
	if err == nil || apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("expected generic rotation error, got %v", err)
	}

	old, err := repository.GetRefreshToken(ctx, "hash-live")
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	if old.RotatedAt != nil {
		t.Fatal("failed rotation must roll back the old token update")
	}
}

func TestConcurrentSessionReads(t *testing.T) {
	ctx := context.Background()
	repository := newTestRepository(t)
	session := testSession()
	if err := repository.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := repository.GetSession(ctx, session.ID); err != nil {
					t.Errorf("GetSession: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
