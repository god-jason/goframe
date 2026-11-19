// Package csrf 提供基于签名的 Double Submit Cookie CSRF 防护。
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/security/crypto"
)

// Protector 负责签发与校验 CSRF Token。
type Protector struct {
	cookieName string
	headerName string
	secret     []byte
	ttl        time.Duration
	secure     bool
}

// New 创建 CSRF 保护器。secret 为空时生成进程内随机密钥。
func New(cfg Config) (*Protector, error) {
	secret := []byte(cfg.Secret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("csrf: generate secret: %w", err)
		}
	}
	return &Protector{
		cookieName: cfg.CookieName,
		headerName: cfg.HeaderName,
		secret:     secret,
		ttl:        cfg.TTL,
		secure:     cfg.Secure,
	}, nil
}

// Token 签发新 Token 并写入 Cookie。
func (p *Protector) Token(c *gin.Context) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("csrf: generate token: %w", err)
	}
	binary.BigEndian.PutUint64(raw[:8], uint64(time.Now().Unix()))

	payload := base64.RawURLEncoding.EncodeToString(raw)
	token := payload + "." + p.sign(payload)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(p.cookieName, token, int(p.ttl.Seconds()), "/", "", p.secure, true)
	return token, nil
}

// Validate 校验请求中的 CSRF Token。
func (p *Protector) Validate(c *gin.Context) error {
	cookie, err := c.Cookie(p.cookieName)
	if err != nil || cookie == "" {
		return apperrors.Forbidden("csrf token missing")
	}
	header := c.GetHeader(p.headerName)
	if header == "" {
		return apperrors.Forbidden("csrf token missing")
	}
	if subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
		return apperrors.Forbidden("csrf token mismatch")
	}

	payload, signature, ok := splitToken(header)
	if !ok {
		return apperrors.Forbidden("invalid csrf token")
	}
	expected := p.sign(payload)
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
		return apperrors.Forbidden("invalid csrf token")
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(raw) < 8 {
		return apperrors.Forbidden("invalid csrf token")
	}
	issuedAt := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	if time.Since(issuedAt) > p.ttl {
		return apperrors.Forbidden("csrf token expired")
	}
	return nil
}

// CookieName 返回 Cookie 名称。
func (p *Protector) CookieName() string { return p.cookieName }

// HeaderName 返回 Header 名称。
func (p *Protector) HeaderName() string { return p.headerName }

func (p *Protector) sign(payload string) string {
	return base64.RawURLEncoding.EncodeToString(crypto.HMACSHA256(p.secret, []byte(payload)))
}

func splitToken(token string) (payload, signature string, ok bool) {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			if i == 0 || i == len(token)-1 {
				return "", "", false
			}
			return token[:i], token[i+1:], true
		}
	}
	return "", "", false
}
