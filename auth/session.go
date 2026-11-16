package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	apperrors "github.com/god-jason/goframe/errors"
)

// Session 是登录会话。
type Session struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	TenantID     string     `json:"tenant_id,omitempty"`
	Roles        []string   `json:"roles,omitempty"`
	Permissions  []string   `json:"permissions,omitempty"`
	DeviceName   string     `json:"device_name,omitempty"`
	IP           string     `json:"ip,omitempty"`
	UserAgent    string     `json:"user_agent,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastActiveAt time.Time  `json:"last_active_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// Active 判断会话是否仍然有效。
func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// Clone 返回会话深拷贝。
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Roles = append([]string(nil), s.Roles...)
	clone.Permissions = append([]string(nil), s.Permissions...)
	if s.RevokedAt != nil {
		t := *s.RevokedAt
		clone.RevokedAt = &t
	}
	return &clone
}

// RefreshToken 是刷新令牌记录。服务端只保存 Token Hash。
type RefreshToken struct {
	ID            string     `json:"id"`
	SessionID     string     `json:"session_id"`
	FamilyID      string     `json:"family_id"`
	TokenHash     string     `json:"token_hash"`
	ParentTokenID string     `json:"parent_token_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RotatedAt     *time.Time `json:"rotated_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

// Clone 返回刷新令牌深拷贝。
func (t *RefreshToken) Clone() *RefreshToken {
	if t == nil {
		return nil
	}
	clone := *t
	if t.RotatedAt != nil {
		v := *t.RotatedAt
		clone.RotatedAt = &v
	}
	if t.RevokedAt != nil {
		v := *t.RevokedAt
		clone.RevokedAt = &v
	}
	return &clone
}

// SessionRepository 是会话与刷新令牌的持久化接口。
// 生产环境应使用数据库实现，框架默认使用内存实现便于开发与测试。
type SessionRepository interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	ListSessions(ctx context.Context, userID string) ([]*Session, error)
	RevokeSession(ctx context.Context, id string, at time.Time) error
	RevokeUserSessions(ctx context.Context, userID string, exceptID string, at time.Time) error

	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error)
	RotateRefreshToken(ctx context.Context, oldID string, next *RefreshToken, at time.Time) error
	RevokeTokenFamily(ctx context.Context, familyID string, at time.Time) error
	RevokeSessionTokens(ctx context.Context, sessionID string, at time.Time) error
}

// MemorySessionRepository 是进程内会话仓库，仅适用于开发与测试。
type MemorySessionRepository struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	tokensByHash map[string]*RefreshToken
	tokensByID   map[string]*RefreshToken
}

// NewMemorySessionRepository 创建内存会话仓库。
func NewMemorySessionRepository() *MemorySessionRepository {
	return &MemorySessionRepository{
		sessions:     make(map[string]*Session),
		tokensByHash: make(map[string]*RefreshToken),
		tokensByID:   make(map[string]*RefreshToken),
	}
}

// CreateSession 创建会话。
func (r *MemorySessionRepository) CreateSession(_ context.Context, session *Session) error {
	if session == nil || session.ID == "" {
		return fmt.Errorf("auth: session id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[session.ID]; exists {
		return apperrors.Conflict("session already exists")
	}
	r.sessions[session.ID] = session.Clone()
	return nil
}

// GetSession 读取会话。
func (r *MemorySessionRepository) GetSession(_ context.Context, id string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[id]
	if !ok {
		return nil, apperrors.NotFound("session not found")
	}
	return session.Clone(), nil
}

// UpdateSession 更新会话。
func (r *MemorySessionRepository) UpdateSession(_ context.Context, session *Session) error {
	if session == nil {
		return fmt.Errorf("auth: session is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[session.ID]; !ok {
		return apperrors.NotFound("session not found")
	}
	r.sessions[session.ID] = session.Clone()
	return nil
}

// ListSessions 列出用户的会话。
func (r *MemorySessionRepository) ListSessions(_ context.Context, userID string) ([]*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sessions []*Session
	for _, session := range r.sessions {
		if session.UserID == userID {
			sessions = append(sessions, session.Clone())
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

// RevokeSession 撤销指定会话。
func (r *MemorySessionRepository) RevokeSession(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	if !ok {
		return apperrors.NotFound("session not found")
	}
	if session.RevokedAt == nil {
		revoked := at
		session.RevokedAt = &revoked
	}
	return nil
}

// RevokeUserSessions 撤销用户的全部会话（可保留指定会话）。
func (r *MemorySessionRepository) RevokeUserSessions(_ context.Context, userID string, exceptID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, session := range r.sessions {
		if session.UserID != userID || id == exceptID {
			continue
		}
		if session.RevokedAt == nil {
			revoked := at
			session.RevokedAt = &revoked
		}
		r.revokeTokensLocked(id, at)
	}
	return nil
}

// CreateRefreshToken 保存刷新令牌。
func (r *MemorySessionRepository) CreateRefreshToken(_ context.Context, token *RefreshToken) error {
	if token == nil || token.TokenHash == "" {
		return fmt.Errorf("auth: refresh token hash is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tokensByHash[token.TokenHash]; exists {
		return apperrors.Conflict("refresh token already exists")
	}
	stored := token.Clone()
	r.tokensByHash[stored.TokenHash] = stored
	r.tokensByID[stored.ID] = stored
	return nil
}

// GetRefreshToken 根据哈希读取刷新令牌。
func (r *MemorySessionRepository) GetRefreshToken(_ context.Context, tokenHash string) (*RefreshToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	token, ok := r.tokensByHash[tokenHash]
	if !ok {
		return nil, apperrors.NotFound("refresh token not found")
	}
	return token.Clone(), nil
}

// RotateRefreshToken 轮换刷新令牌，确保旧令牌只可使用一次。
func (r *MemorySessionRepository) RotateRefreshToken(_ context.Context, oldID string, next *RefreshToken, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	old, ok := r.tokensByID[oldID]
	if !ok {
		return apperrors.NotFound("refresh token not found")
	}
	if old.RotatedAt != nil {
		return apperrors.Conflict("refresh token already rotated")
	}
	rotated := at
	old.RotatedAt = &rotated

	stored := next.Clone()
	r.tokensByHash[stored.TokenHash] = stored
	r.tokensByID[stored.ID] = stored
	return nil
}

// RevokeTokenFamily 撤销整个 Token Family（检测到重放时使用）。
func (r *MemorySessionRepository) RevokeTokenFamily(_ context.Context, familyID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range r.tokensByID {
		if token.FamilyID == familyID && token.RevokedAt == nil {
			revoked := at
			token.RevokedAt = &revoked
		}
	}
	return nil
}

// RevokeSessionTokens 撤销会话关联的全部刷新令牌。
func (r *MemorySessionRepository) RevokeSessionTokens(_ context.Context, sessionID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokeTokensLocked(sessionID, at)
	return nil
}

func (r *MemorySessionRepository) revokeTokensLocked(sessionID string, at time.Time) {
	for _, token := range r.tokensByID {
		if token.SessionID == sessionID && token.RevokedAt == nil {
			revoked := at
			token.RevokedAt = &revoked
		}
	}
}

// newOpaqueToken 生成高强度随机刷新令牌。
func newOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashToken 计算刷新令牌哈希。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
