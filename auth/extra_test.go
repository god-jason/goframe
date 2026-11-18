package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
)

// flakySessionRepository 在内存仓库基础上按需注入错误。
type flakySessionRepository struct {
	*MemorySessionRepository

	createErr     error
	getErr        error
	updateErr     error
	listErr       error
	revokeErr     error
	revokeUserErr error

	createTokenErr  error
	getTokenErr     error
	rotateErr       error
	revokeFamilyErr error
	revokeTokensErr error
}

func newFlakySessionRepository() *flakySessionRepository {
	return &flakySessionRepository{MemorySessionRepository: NewMemorySessionRepository()}
}

func (f *flakySessionRepository) CreateSession(ctx context.Context, session *Session) error {
	if f.createErr != nil {
		return f.createErr
	}
	return f.MemorySessionRepository.CreateSession(ctx, session)
}

func (f *flakySessionRepository) GetSession(ctx context.Context, id string) (*Session, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.MemorySessionRepository.GetSession(ctx, id)
}

func (f *flakySessionRepository) UpdateSession(ctx context.Context, session *Session) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	return f.MemorySessionRepository.UpdateSession(ctx, session)
}

func (f *flakySessionRepository) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.MemorySessionRepository.ListSessions(ctx, userID)
}

func (f *flakySessionRepository) RevokeSession(ctx context.Context, id string, at time.Time) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	return f.MemorySessionRepository.RevokeSession(ctx, id, at)
}

func (f *flakySessionRepository) RevokeUserSessions(ctx context.Context, userID, exceptID string, at time.Time) error {
	if f.revokeUserErr != nil {
		return f.revokeUserErr
	}
	return f.MemorySessionRepository.RevokeUserSessions(ctx, userID, exceptID, at)
}

func (f *flakySessionRepository) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	if f.createTokenErr != nil {
		return f.createTokenErr
	}
	return f.MemorySessionRepository.CreateRefreshToken(ctx, token)
}

func (f *flakySessionRepository) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	if f.getTokenErr != nil {
		return nil, f.getTokenErr
	}
	return f.MemorySessionRepository.GetRefreshToken(ctx, tokenHash)
}

func (f *flakySessionRepository) RotateRefreshToken(ctx context.Context, oldID string, next *RefreshToken, at time.Time) error {
	if f.rotateErr != nil {
		return f.rotateErr
	}
	return f.MemorySessionRepository.RotateRefreshToken(ctx, oldID, next, at)
}

func (f *flakySessionRepository) RevokeTokenFamily(ctx context.Context, familyID string, at time.Time) error {
	if f.revokeFamilyErr != nil {
		return f.revokeFamilyErr
	}
	return f.MemorySessionRepository.RevokeTokenFamily(ctx, familyID, at)
}

func (f *flakySessionRepository) RevokeSessionTokens(ctx context.Context, sessionID string, at time.Time) error {
	if f.revokeTokensErr != nil {
		return f.revokeTokensErr
	}
	return f.MemorySessionRepository.RevokeSessionTokens(ctx, sessionID, at)
}

func newTestService(t *testing.T, repo SessionRepository) *Service {
	t.Helper()
	service, err := NewService(testAuthConfig(), repo, logger.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func TestNewServiceDefaultsAndErrors(t *testing.T) {
	cfg := testAuthConfig()
	cfg.Secret = ""
	service, err := NewService(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if service.repo == nil || service.log == nil {
		t.Fatal("defaults must be filled in")
	}
	if !service.jwt.GeneratedSecret() {
		t.Fatal("expected generated secret")
	}

	cfg = testAuthConfig()
	cfg.AccessTokenTTL = 0
	if _, err := NewService(cfg, nil, nil); err == nil {
		t.Fatal("expected invalid ttl error")
	}
}

func TestAuthenticateRejectsInvalidTokens(t *testing.T) {
	ctx := context.Background()
	repo := newFlakySessionRepository()
	service := newTestService(t, repo)

	// 没有 session id 的访问令牌。
	token, _, err := service.jwt.IssueAccessToken(&Identity{UserID: "u1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := service.Authenticate(ctx, token); err == nil {
		t.Fatal("expected token without session id to be rejected")
	}

	// 仓库读取失败。
	token, _, err = service.jwt.IssueAccessToken(&Identity{UserID: "u1", SessionID: "s1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	repo.getErr = stderrors.New("db down")
	if _, err := service.Authenticate(ctx, token); err == nil {
		t.Fatal("expected repository error")
	}
	repo.getErr = nil

	// 会话用户与令牌用户不一致。
	now := time.Now().UTC()
	if err := repo.CreateSession(ctx, &Session{ID: "s1", UserID: "u2", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := service.Authenticate(ctx, token); err == nil {
		t.Fatal("expected user mismatch to be rejected")
	}

	// 会话已撤销。
	revokedAt := now
	if err := repo.UpdateSession(ctx, &Session{ID: "s1", UserID: "u1", CreatedAt: now, ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	_, err = service.Authenticate(ctx, token)
	if got := apperrors.From(err); got.Code != apperrors.CodeSessionRevoked {
		t.Fatalf("code = %d, want %d", got.Code, apperrors.CodeSessionRevoked)
	}
}

func TestLoginErrorPaths(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, NewMemorySessionRepository())

	if _, _, err := service.Login(ctx, nil, SessionMeta{}); err == nil {
		t.Fatal("expected nil identity error")
	}
	if _, _, err := service.Login(ctx, &Identity{}, SessionMeta{}); err == nil {
		t.Fatal("expected empty user id error")
	}

	failingCreate := newFlakySessionRepository()
	failingCreate.createErr = stderrors.New("insert failed")
	service = newTestService(t, failingCreate)
	if _, _, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{}); err == nil {
		t.Fatal("expected create session error")
	}

	failingToken := newFlakySessionRepository()
	failingToken.createTokenErr = stderrors.New("token insert failed")
	service = newTestService(t, failingToken)
	if _, _, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{}); err == nil {
		t.Fatal("expected create refresh token error")
	}
	// 失败时应撤销刚创建的会话。
	sessions, err := failingToken.ListSessions(ctx, "u1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].RevokedAt == nil {
		t.Fatalf("expected login session to be revoked, got %+v", sessions)
	}
}

func TestLoginSetsDefaultAuthType(t *testing.T) {
	service := newTestService(t, NewMemorySessionRepository())
	_, identity, err := service.Login(context.Background(), &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if identity.AuthType != "user" {
		t.Fatalf("auth type = %q", identity.AuthType)
	}
}

func TestRefreshErrorPaths(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	service := newTestService(t, NewMemorySessionRepository())
	if _, _, err := service.Refresh(ctx, "", SessionMeta{}); err == nil {
		t.Fatal("expected missing refresh token error")
	}
	if _, _, err := service.Refresh(ctx, "unknown", SessionMeta{}); err == nil {
		t.Fatal("expected unknown refresh token error")
	}

	// 已撤销的刷新令牌。
	repo := newFlakySessionRepository()
	token := &RefreshToken{
		ID: "r1", SessionID: "s1", FamilyID: "f1",
		TokenHash: hashToken("revoked"), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	revokedAt := now
	token.RevokedAt = &revokedAt
	if err := repo.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	service = newTestService(t, repo)
	_, _, err := service.Refresh(ctx, "revoked", SessionMeta{})
	if got := apperrors.From(err); got.Code != apperrors.CodeSessionRevoked {
		t.Fatalf("code = %d, want %d", got.Code, apperrors.CodeSessionRevoked)
	}

	// 已过期的刷新令牌。
	repo = newFlakySessionRepository()
	expired := &RefreshToken{
		ID: "r2", SessionID: "s2", FamilyID: "f2",
		TokenHash: hashToken("expired"), CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := repo.CreateRefreshToken(ctx, expired); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	service = newTestService(t, repo)
	if _, _, err := service.Refresh(ctx, "expired", SessionMeta{}); err == nil {
		t.Fatal("expected expired refresh token error")
	}

	// 会话读取失败。
	repo = newFlakySessionRepository()
	if err := repo.CreateRefreshToken(ctx, &RefreshToken{
		ID: "r3", SessionID: "s3", FamilyID: "f3",
		TokenHash: hashToken("no-session"), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	repo.getErr = stderrors.New("db down")
	service = newTestService(t, repo)
	if _, _, err := service.Refresh(ctx, "no-session", SessionMeta{}); err == nil {
		t.Fatal("expected session read error")
	}

	// 会话已撤销。
	repo = newFlakySessionRepository()
	if err := repo.CreateSession(ctx, &Session{ID: "s4", UserID: "u4", CreatedAt: now, ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := repo.CreateRefreshToken(ctx, &RefreshToken{
		ID: "r4", SessionID: "s4", FamilyID: "f4",
		TokenHash: hashToken("revoked-session"), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	service = newTestService(t, repo)
	if _, _, err := service.Refresh(ctx, "revoked-session", SessionMeta{}); err == nil {
		t.Fatal("expected inactive session error")
	}
}

func TestRefreshRotateAndUpdateErrors(t *testing.T) {
	ctx := context.Background()

	repo := newFlakySessionRepository()
	service := newTestService(t, repo)
	tokens, _, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	repo.rotateErr = stderrors.New("rotate failed")
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, SessionMeta{}); err == nil {
		t.Fatal("expected rotate error")
	}

	// 轮换成功但更新会话失败时只记录警告。
	repo = newFlakySessionRepository()
	service = newTestService(t, repo)
	tokens, _, err = service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	repo.updateErr = stderrors.New("update failed")
	rotated, identity, err := service.Refresh(ctx, tokens.RefreshToken, SessionMeta{
		DeviceName: "laptop",
		IP:         "10.0.0.1",
		UserAgent:  "test-agent",
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.AccessToken == "" || identity.UserID != "u1" {
		t.Fatalf("unexpected refresh result: %+v %+v", rotated, identity)
	}
}

func TestLogoutListAndRevokeErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	service := newTestService(t, NewMemorySessionRepository())
	if err := service.Logout(ctx, ""); err != nil {
		t.Fatalf("empty logout: %v", err)
	}
	if err := service.Logout(ctx, "missing"); err != nil {
		t.Fatalf("missing logout should be ignored: %v", err)
	}

	repo := newFlakySessionRepository()
	repo.revokeErr = stderrors.New("revoke failed")
	service = newTestService(t, repo)
	if err := service.Logout(ctx, "s1"); err == nil {
		t.Fatal("expected revoke error")
	}

	repo = newFlakySessionRepository()
	if err := repo.CreateSession(ctx, &Session{ID: "s1", UserID: "u1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	repo.revokeTokensErr = stderrors.New("token revoke failed")
	service = newTestService(t, repo)
	if err := service.Logout(ctx, "s1"); err == nil {
		t.Fatal("expected token revoke error")
	}

	// ListSessions。
	if _, err := service.ListSessions(ctx, ""); err == nil {
		t.Fatal("expected empty user id error")
	}
	repo.listErr = stderrors.New("list failed")
	if _, err := service.ListSessions(ctx, "u1"); err == nil {
		t.Fatal("expected list error")
	}
	repo.listErr = nil
	if sessions, err := service.ListSessions(ctx, "u1"); err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %v, %v", sessions, err)
	}

	// RevokeSession。
	if err := service.RevokeSession(ctx, "u1", "missing"); err == nil {
		t.Fatal("expected missing session error")
	}
	repo.getErr = stderrors.New("db down")
	if err := service.RevokeSession(ctx, "u1", "s1"); err == nil {
		t.Fatal("expected get error")
	}
	repo.getErr = nil
	if err := service.RevokeSession(ctx, "other", "s1"); err == nil {
		t.Fatal("expected user mismatch error")
	}

	repo.revokeErr = stderrors.New("revoke failed")
	if err := service.RevokeSession(ctx, "u1", "s1"); err == nil {
		t.Fatal("expected revoke error")
	}
	repo.revokeErr = nil
	repo.revokeTokensErr = stderrors.New("token revoke failed")
	if err := service.RevokeSession(ctx, "u1", "s1"); err == nil {
		t.Fatal("expected token revoke error")
	}
	repo.revokeTokensErr = nil
	if err := service.RevokeSession(ctx, "u1", "s1"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	// RevokeAllSessions。
	if err := service.RevokeAllSessions(ctx, "", ""); err == nil {
		t.Fatal("expected empty user id error")
	}
	repo.revokeUserErr = stderrors.New("bulk revoke failed")
	if err := service.RevokeAllSessions(ctx, "u1", ""); err == nil {
		t.Fatal("expected bulk revoke error")
	}
	repo.revokeUserErr = nil
	if err := service.RevokeAllSessions(ctx, "u1", ""); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
}

func TestAPIKeyCloneAndValidation(t *testing.T) {
	var nilKey *APIKey
	if nilKey.Clone() != nil {
		t.Fatal("cloning nil api key must return nil")
	}

	expires := time.Now().Add(time.Hour)
	revoked := time.Now()
	used := time.Now()
	key := &APIKey{
		ID: "k1", Roles: []string{"r1"}, Permissions: []string{"p1"},
		ExpiresAt: &expires, RevokedAt: &revoked, LastUsedAt: &used,
	}
	clone := key.Clone()
	clone.Roles[0] = "changed"
	clone.Permissions[0] = "changed"
	*clone.ExpiresAt = expires.Add(time.Hour)
	if key.Roles[0] != "r1" || key.Permissions[0] != "p1" || !key.ExpiresAt.Equal(expires) {
		t.Fatal("Clone must deep copy fields")
	}
	if key.Valid(time.Now()) {
		t.Fatal("revoked key must not be valid")
	}

	noExpiry := &APIKey{}
	if !noExpiry.Valid(time.Now()) {
		t.Fatal("key without expiry must be valid")
	}

	if _, _, err := NewAPIKey("", "u1", "", nil, nil, 0); err == nil {
		t.Fatal("expected empty name error")
	}
	if _, _, err := NewAPIKey("name", "", "", nil, nil, 0); err == nil {
		t.Fatal("expected empty user id error")
	}
	plain, key, err := NewAPIKey("name", "u1", "", nil, nil, 0)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if key.ExpiresAt != nil {
		t.Fatal("ttl<=0 must not set expiration")
	}
	identity := key.Identity()
	if identity.AuthType != "api_key" || identity.UserID != "u1" {
		t.Fatalf("identity = %+v", identity)
	}
	if len(plain) <= apiKeyPrefixLen {
		t.Fatalf("plain key too short: %q", plain)
	}
}

type failingAPIKeyRepository struct {
	*MemoryAPIKeyRepository
	getErr error
}

func (f *failingAPIKeyRepository) GetByPrefix(ctx context.Context, prefix string) (*APIKey, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.MemoryAPIKeyRepository.GetByPrefix(ctx, prefix)
}

func TestAPIKeyRepositoryErrorsAndList(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryAPIKeyRepository()
	SetAPIKeyRepository(repo)
	t.Cleanup(func() { SetAPIKeyRepository(nil) })

	if err := repo.Create(ctx, nil); err == nil {
		t.Fatal("expected nil key error")
	}
	if err := repo.Create(ctx, &APIKey{}); err == nil {
		t.Fatal("expected empty id error")
	}

	plain, key, err := NewAPIKey("name", "u1", "t1", []string{"r1"}, []string{"p1"}, time.Hour)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if err := repo.Create(ctx, key); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := VerifyAPIKey(ctx, plain); err != nil {
		t.Fatalf("VerifyAPIKey: %v", err)
	}

	keys, err := repo.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("keys = %+v", keys)
	}
	if other, err := repo.List(ctx, "u2"); err != nil || len(other) != 0 {
		t.Fatalf("List(u2) = %v, %v", other, err)
	}

	if err := repo.UpdateLastUsed(ctx, "missing", time.Now()); err == nil {
		t.Fatal("expected missing key error")
	}
	if err := repo.Revoke(ctx, "missing", time.Now()); err == nil {
		t.Fatal("expected missing key error")
	}

	failing := &failingAPIKeyRepository{MemoryAPIKeyRepository: repo, getErr: stderrors.New("db down")}
	SetAPIKeyRepository(failing)
	if _, err := VerifyAPIKey(ctx, plain); err == nil {
		t.Fatal("expected repository error")
	}
}

func TestJWTIssueAndParseErrors(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	if _, _, err := manager.IssueAccessToken(nil); err == nil {
		t.Fatal("expected nil identity error")
	}
	if _, _, err := manager.IssueAccessToken(&Identity{}); err == nil {
		t.Fatal("expected empty user id error")
	}

	// 非 HMAC 算法签名（EdDSA）必须在 keyfunc 阶段被拒绝。
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
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
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign eddsa token: %v", err)
	}
	if _, err := manager.ParseAccessToken(token); err == nil {
		t.Fatal("expected non-HMAC algorithm to be rejected")
	}
}

func TestPasswordHasherErrors(t *testing.T) {
	if _, err := NewPasswordHasher(PasswordConfig{Memory: 1024, Iterations: 1, Parallelism: 1}); err == nil {
		t.Fatal("expected small memory error")
	}
	if _, err := NewPasswordHasher(PasswordConfig{Memory: 8192, Iterations: 0, Parallelism: 1}); err == nil {
		t.Fatal("expected iterations error")
	}
	if _, err := NewPasswordHasher(PasswordConfig{Memory: 8192, Iterations: 1, Parallelism: 0}); err == nil {
		t.Fatal("expected parallelism error")
	}

	hasher, err := NewPasswordHasher(PasswordConfig{Memory: 8192, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	if _, err := hasher.Hash(""); err == nil {
		t.Fatal("expected empty password error")
	}

	hash, err := hasher.Hash("secret")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	parts := strings.Split(hash, "$")

	badVersion := append([]string(nil), parts...)
	badVersion[2] = "v=99"
	if _, err := hasher.Verify("secret", strings.Join(badVersion, "$")); err == nil {
		t.Fatal("expected version error")
	}

	badParams := append([]string(nil), parts...)
	badParams[3] = "broken"
	if _, err := hasher.Verify("secret", strings.Join(badParams, "$")); err == nil {
		t.Fatal("expected params error")
	}

	badSalt := append([]string(nil), parts...)
	badSalt[4] = "!!!"
	if _, err := hasher.Verify("secret", strings.Join(badSalt, "$")); err == nil {
		t.Fatal("expected salt error")
	}

	badValue := append([]string(nil), parts...)
	badValue[5] = "!!!"
	if _, err := hasher.Verify("secret", strings.Join(badValue, "$")); err == nil {
		t.Fatal("expected value error")
	}

	notArgon2 := append([]string(nil), parts...)
	notArgon2[1] = "scrypt"
	if _, err := hasher.Verify("secret", strings.Join(notArgon2, "$")); err == nil {
		t.Fatal("expected algorithm error")
	}
}

func TestSessionCloneNilAndRepositoryValidation(t *testing.T) {
	var nilSession *Session
	if nilSession.Clone() != nil {
		t.Fatal("nil session clone must be nil")
	}
	var nilToken *RefreshToken
	if nilToken.Clone() != nil {
		t.Fatal("nil token clone must be nil")
	}

	repo := NewMemorySessionRepository()
	ctx := context.Background()
	if err := repo.CreateSession(ctx, nil); err == nil {
		t.Fatal("expected nil session error")
	}
	if err := repo.CreateSession(ctx, &Session{}); err == nil {
		t.Fatal("expected empty session id error")
	}
	if err := repo.UpdateSession(ctx, nil); err == nil {
		t.Fatal("expected nil session update error")
	}
	if err := repo.CreateRefreshToken(ctx, nil); err == nil {
		t.Fatal("expected nil token error")
	}
	if err := repo.CreateRefreshToken(ctx, &RefreshToken{}); err == nil {
		t.Fatal("expected empty token hash error")
	}
	if err := repo.RotateRefreshToken(ctx, "missing", &RefreshToken{ID: "next", TokenHash: "h"}, time.Now()); err == nil {
		t.Fatal("expected missing token error")
	}

	// RevokeTokenFamily 与 RevokeSessionTokens 对不存在的数据应为无操作。
	if err := repo.RevokeTokenFamily(ctx, "missing", time.Now()); err != nil {
		t.Fatalf("RevokeTokenFamily: %v", err)
	}
	if err := repo.RevokeSessionTokens(ctx, "missing", time.Now()); err != nil {
		t.Fatalf("RevokeSessionTokens: %v", err)
	}

	// 已撤销的令牌再次撤销不应改变时间。
	now := time.Now().UTC()
	token := &RefreshToken{ID: "r1", SessionID: "s1", FamilyID: "f1", TokenHash: "h1", ExpiresAt: now.Add(time.Hour)}
	if err := repo.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := repo.RevokeSessionTokens(ctx, "s1", now); err != nil {
		t.Fatalf("RevokeSessionTokens: %v", err)
	}
	first, _ := repo.GetRefreshToken(ctx, "h1")
	if err := repo.RevokeSessionTokens(ctx, "s1", now.Add(time.Minute)); err != nil {
		t.Fatalf("RevokeSessionTokens: %v", err)
	}
	second, _ := repo.GetRefreshToken(ctx, "h1")
	if first.RevokedAt == nil || !first.RevokedAt.Equal(*second.RevokedAt) {
		t.Fatal("already revoked token must keep its revocation time")
	}
}

func TestTOTPBoundaries(t *testing.T) {
	at := time.Unix(1111111111, 0)
	code, err := TOTPCode(rfc6238Secret, at, 0, 0)
	if err != nil {
		t.Fatalf("TOTPCode defaults: %v", err)
	}
	if len(code) != DefaultTOTPDigits {
		t.Fatalf("code length = %d", len(code))
	}

	if VerifyTOTP(rfc6238Secret, code, at, 11, 30, 0) {
		t.Fatal("expected too many digits to be rejected")
	}
	if !VerifyTOTP(rfc6238Secret, code, at, 6, 30, -1) {
		t.Fatal("negative skew should be treated as zero")
	}
	if !VerifyTOTP(rfc6238Secret, code, at, 6, 30, 100) {
		t.Fatal("large skew should be clamped")
	}
	if VerifyTOTP(rfc6238Secret, code, time.Unix(-1, 0), 6, 30, 0) {
		t.Fatal("negative time must be rejected")
	}
	if VerifyTOTP(rfc6238Secret, "12345", at, 6, 30, 0) {
		t.Fatal("short code must be rejected")
	}

	// skew 覆盖到负计数时跳过。
	early, err := TOTPCode(rfc6238Secret, time.Unix(0, 0), 6, 30)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if !VerifyTOTP(rfc6238Secret, early, time.Unix(0, 0), 6, 30, 2) {
		t.Fatal("expected current code to verify")
	}

	// 无填充的短密钥走第二解码分支。
	if _, err := TOTPCode("AA", at, 6, 30); err != nil {
		t.Fatalf("TOTPCode no padding: %v", err)
	}

	url := OTPAuthURL("", "alice", rfc6238Secret+"=", 0, 0)
	if !strings.Contains(url, "otpauth://totp/alice?") {
		t.Fatalf("unexpected url without issuer: %s", url)
	}
	if !strings.Contains(url, "digits=6") || !strings.Contains(url, "period=30") {
		t.Fatalf("expected default digits/period: %s", url)
	}

	if _, err := GenerateRecoveryCodes(1001); err == nil {
		t.Fatal("expected too many recovery codes error")
	}
	codes, err := GenerateRecoveryCodes(1)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 1 {
		t.Fatalf("codes = %v", codes)
	}
}
