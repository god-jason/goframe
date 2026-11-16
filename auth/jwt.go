package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/id"
)

// Claims 是访问令牌声明。
type Claims struct {
	jwt.RegisteredClaims
	UserID      string   `json:"uid"`
	TenantID    string   `json:"tid,omitempty"`
	SessionID   string   `json:"sid,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"perms,omitempty"`
	AuthType    string   `json:"typ,omitempty"`
}

// JWTManager 负责签发与校验访问令牌。
// 只允许 HS256，禁止 none 算法；校验 iss、aud、exp、nbf、iat。
type JWTManager struct {
	secret    []byte
	generated bool
	issuer    string
	audience  string
	ttl       time.Duration
	leeway    time.Duration
}

// NewJWTManager 创建 JWT 管理器。未配置 Secret 时生成进程内随机密钥，
// 生产环境必须显式配置足够强度的 Secret。
func NewJWTManager(cfg Config) (*JWTManager, error) {
	secret := []byte(cfg.Secret)
	generated := false
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("auth: generate jwt secret: %w", err)
		}
		generated = true
	}
	if cfg.AccessTokenTTL <= 0 {
		return nil, fmt.Errorf("auth: access token ttl must be greater than 0")
	}
	return &JWTManager{
		secret:    secret,
		generated: generated,
		issuer:    cfg.Issuer,
		audience:  cfg.Audience,
		ttl:       cfg.AccessTokenTTL,
		leeway:    cfg.Leeway,
	}, nil
}

// GeneratedSecret 表示当前使用的是进程内随机密钥，重启后 Token 将失效。
func (m *JWTManager) GeneratedSecret() bool { return m.generated }

// IssueAccessToken 签发访问令牌。
func (m *JWTManager) IssueAccessToken(identity *Identity) (string, time.Time, error) {
	if identity == nil || identity.UserID == "" {
		return "", time.Time{}, fmt.Errorf("auth: user id is required")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(m.ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   identity.UserID,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        id.New(),
		},
		UserID:      identity.UserID,
		TenantID:    identity.TenantID,
		SessionID:   identity.SessionID,
		Roles:       identity.Roles,
		Permissions: identity.Permissions,
		AuthType:    identity.AuthType,
	}
	if m.audience != "" {
		claims.Audience = jwt.ClaimStrings{m.audience}
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign access token: %w", err)
	}
	return token, expiresAt, nil
}

// ParseAccessToken 校验并解析访问令牌。
func (m *JWTManager) ParseAccessToken(tokenString string) (*Identity, error) {
	if tokenString == "" {
		return nil, apperrors.Unauthorized("missing access token")
	}

	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	}
	if m.leeway > 0 {
		options = append(options, jwt.WithLeeway(m.leeway))
	}
	if m.issuer != "" {
		options = append(options, jwt.WithIssuer(m.issuer))
	}
	if m.audience != "" {
		options = append(options, jwt.WithAudience(m.audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		return m.secret, nil
	}, options...)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, apperrors.TokenExpired()
		}
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token").WithCause(err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token")
	}
	if claims.UserID == "" {
		return nil, apperrors.New(apperrors.CodeTokenInvalid, http.StatusUnauthorized, "invalid token")
	}

	return &Identity{
		UserID:      claims.UserID,
		TenantID:    claims.TenantID,
		SessionID:   claims.SessionID,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
		AuthType:    claims.AuthType,
	}, nil
}
