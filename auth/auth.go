package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/god-jason/goframe/config"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/id"
	"github.com/god-jason/goframe/logger"
)

// Authenticator 是认证接口：认证解决“当前请求是谁发起的”。
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*Identity, error)
}

// Manager 是完整的认证管理接口，业务登录流程通过它签发与撤销令牌。
type Manager interface {
	Authenticator
	Login(ctx context.Context, identity *Identity, meta SessionMeta) (*Tokens, *Identity, error)
	Refresh(ctx context.Context, refreshToken string, meta SessionMeta) (*Tokens, *Identity, error)
	Logout(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, userID string) ([]*Session, error)
	RevokeSession(ctx context.Context, userID, sessionID string) error
	RevokeAllSessions(ctx context.Context, userID, exceptSessionID string) error
}

// Tokens 是登录/刷新返回的令牌对。
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// SessionMeta 是会话元信息。
type SessionMeta struct {
	DeviceName string
	IP         string
	UserAgent  string
}

// Service 是认证服务实现。
type Service struct {
	cfg  config.AuthConfig
	jwt  *JWTManager
	repo SessionRepository
	log  logger.Logger
}

// NewService 创建认证服务。
func NewService(cfg config.AuthConfig, repo SessionRepository, log logger.Logger) (*Service, error) {
	if log == nil {
		log = logger.NewNop()
	}
	if repo == nil {
		repo = NewMemorySessionRepository()
	}
	jwtManager, err := NewJWTManager(cfg)
	if err != nil {
		return nil, err
	}
	if jwtManager.GeneratedSecret() {
		log.Warn("auth: jwt secret is not configured, using an ephemeral secret")
	}
	return &Service{cfg: cfg, jwt: jwtManager, repo: repo, log: log}, nil
}

// Authenticate 校验访问令牌并确认会话仍然有效。
func (s *Service) Authenticate(ctx context.Context, token string) (*Identity, error) {
	identity, err := s.jwt.ParseAccessToken(token)
	if err != nil {
		return nil, err
	}
	if identity.SessionID == "" {
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token")
	}

	session, err := s.repo.GetSession(ctx, identity.SessionID)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token").WithCause(err)
	}
	if session.UserID != identity.UserID {
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token")
	}
	if !session.Active(time.Now().UTC()) {
		return nil, apperrors.New(apperrors.CodeSessionRevoked, http.StatusUnauthorized, "session revoked")
	}
	return identity, nil
}

// Login 创建会话并签发令牌。identity 由业务在验证用户凭据后构造。
func (s *Service) Login(ctx context.Context, identity *Identity, meta SessionMeta) (*Tokens, *Identity, error) {
	if identity == nil || identity.UserID == "" {
		return nil, nil, apperrors.BadRequest("user id is required")
	}

	now := time.Now().UTC()
	session := &Session{
		ID:           id.New(),
		UserID:       identity.UserID,
		TenantID:     identity.TenantID,
		Roles:        append([]string(nil), identity.Roles...),
		Permissions:  append([]string(nil), identity.Permissions...),
		DeviceName:   meta.DeviceName,
		IP:           meta.IP,
		UserAgent:    meta.UserAgent,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(s.cfg.RefreshTokenTTL),
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, nil, err
	}

	current := identity.Clone()
	current.SessionID = session.ID
	if current.AuthType == "" {
		current.AuthType = "user"
	}

	tokens, token, err := s.issueTokens(ctx, now, session, current, "", "")
	if err != nil {
		_ = s.repo.RevokeSession(ctx, session.ID, now)
		return nil, nil, err
	}
	if err := s.repo.CreateRefreshToken(ctx, token); err != nil {
		_ = s.repo.RevokeSession(ctx, session.ID, now)
		return nil, nil, err
	}
	s.log.Info("user logged in",
		logger.String("user_id", current.UserID),
		logger.String("tenant_id", current.TenantID),
		logger.String("session_id", session.ID),
		logger.String("ip", meta.IP),
		logger.String("device", meta.DeviceName),
	)
	return tokens, current, nil
}

// Refresh 轮换刷新令牌。检测到已轮换令牌被再次使用时会撤销整个 Token Family。
func (s *Service) Refresh(ctx context.Context, refreshToken string, meta SessionMeta) (*Tokens, *Identity, error) {
	now := time.Now().UTC()
	if refreshToken == "" {
		return nil, nil, apperrors.Unauthorized("missing refresh token")
	}

	stored, err := s.repo.GetRefreshToken(ctx, hashToken(refreshToken))
	if err != nil {
		return nil, nil, apperrors.Unauthorized("invalid refresh token").WithCause(err)
	}
	if stored.RotatedAt != nil {
		_ = s.repo.RevokeTokenFamily(ctx, stored.FamilyID, now)
		s.log.Error("auth: refresh token reuse detected",
			logger.String("session_id", stored.SessionID),
			logger.String("family_id", stored.FamilyID),
		)
		return nil, nil, apperrors.Unauthorized("invalid refresh token")
	}
	if stored.RevokedAt != nil {
		return nil, nil, apperrors.New(apperrors.CodeSessionRevoked, http.StatusUnauthorized, "session revoked")
	}
	if !now.Before(stored.ExpiresAt) {
		return nil, nil, apperrors.Unauthorized("refresh token expired")
	}

	session, err := s.repo.GetSession(ctx, stored.SessionID)
	if err != nil {
		return nil, nil, apperrors.Unauthorized("invalid refresh token").WithCause(err)
	}
	if !session.Active(now) {
		return nil, nil, apperrors.New(apperrors.CodeSessionRevoked, http.StatusUnauthorized, "session revoked")
	}

	identity := &Identity{
		UserID:      session.UserID,
		TenantID:    session.TenantID,
		SessionID:   session.ID,
		Roles:       append([]string(nil), session.Roles...),
		Permissions: append([]string(nil), session.Permissions...),
		AuthType:    "user",
	}
	tokens, next, err := s.issueTokens(ctx, now, session, identity, stored.FamilyID, stored.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.RotateRefreshToken(ctx, stored.ID, next, now); err != nil {
		_ = s.repo.RevokeTokenFamily(ctx, stored.FamilyID, now)
		return nil, nil, apperrors.Unauthorized("invalid refresh token").WithCause(err)
	}

	session.LastActiveAt = now
	if meta.DeviceName != "" {
		session.DeviceName = meta.DeviceName
	}
	if meta.IP != "" {
		session.IP = meta.IP
	}
	if meta.UserAgent != "" {
		session.UserAgent = meta.UserAgent
	}
	if err := s.repo.UpdateSession(ctx, session); err != nil {
		s.log.Warn("auth: update session failed", logger.Error(err))
	}
	s.log.Info("token refreshed",
		logger.String("user_id", identity.UserID),
		logger.String("session_id", session.ID),
		logger.String("ip", meta.IP),
	)
	return tokens, identity, nil
}

// Logout 撤销当前会话。
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	now := time.Now().UTC()
	if err := s.repo.RevokeSession(ctx, sessionID, now); err != nil {
		if apperrors.Is(err, apperrors.CodeNotFound) {
			return nil
		}
		return err
	}
	if err := s.repo.RevokeSessionTokens(ctx, sessionID, now); err != nil {
		return err
	}
	s.log.Info("user logged out", logger.String("session_id", sessionID))
	return nil
}

// ListSessions 列出用户的全部会话。
func (s *Service) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	if userID == "" {
		return nil, apperrors.BadRequest("user id is required")
	}
	return s.repo.ListSessions(ctx, userID)
}

// RevokeSession 撤销用户指定的会话。
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	session, err := s.repo.GetSession(ctx, sessionID)
	if err != nil || session.UserID != userID {
		return apperrors.NotFound("session not found")
	}
	now := time.Now().UTC()
	if err := s.repo.RevokeSession(ctx, sessionID, now); err != nil {
		return err
	}
	if err := s.repo.RevokeSessionTokens(ctx, sessionID, now); err != nil {
		return err
	}
	s.log.Info("session revoked",
		logger.String("user_id", userID),
		logger.String("session_id", sessionID),
	)
	return nil
}

// RevokeAllSessions 撤销用户的全部会话，可保留当前会话。
func (s *Service) RevokeAllSessions(ctx context.Context, userID, exceptSessionID string) error {
	if userID == "" {
		return apperrors.BadRequest("user id is required")
	}
	now := time.Now().UTC()
	if err := s.repo.RevokeUserSessions(ctx, userID, exceptSessionID, now); err != nil {
		return err
	}
	s.log.Info("all user sessions revoked",
		logger.String("user_id", userID),
		logger.String("except_session_id", exceptSessionID),
	)
	return nil
}

func (s *Service) issueTokens(ctx context.Context, now time.Time, session *Session, identity *Identity, familyID, parentTokenID string) (*Tokens, *RefreshToken, error) {
	accessToken, _, err := s.jwt.IssueAccessToken(identity)
	if err != nil {
		return nil, nil, err
	}

	refreshToken, err := newOpaqueToken()
	if err != nil {
		return nil, nil, err
	}
	if familyID == "" {
		familyID = id.New()
	}

	token := &RefreshToken{
		ID:            id.New(),
		SessionID:     session.ID,
		FamilyID:      familyID,
		TokenHash:     hashToken(refreshToken),
		ParentTokenID: parentTokenID,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.cfg.RefreshTokenTTL),
	}
	tokens := &Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.cfg.AccessTokenTTL.Seconds()),
	}
	_ = ctx
	return tokens, token, nil
}
