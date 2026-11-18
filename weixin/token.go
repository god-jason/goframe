package weixin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/god-jason/goframe/cache"
)

// cachedToken 是缓存中的令牌结构。
type cachedToken struct {
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
}

// tokenStore 负责令牌的本地缓存、共享缓存与并发刷新。
type tokenStore struct {
	cache cache.Cache
	key   string
	group singleflight.Group

	mu        sync.RWMutex
	value     string
	expiresAt time.Time
}

func newTokenStore(value cache.Cache, key string) *tokenStore {
	return &tokenStore{cache: value, key: key}
}

// get 返回有效令牌，必要时调用 fetch 刷新。
func (s *tokenStore) get(ctx context.Context, fetch func(context.Context) (string, time.Duration, error)) (string, error) {
	if value := s.load(ctx); value != "" {
		return value, nil
	}

	value, err, _ := s.group.Do("token", func() (any, error) {
		if value := s.load(ctx); value != "" {
			return value, nil
		}
		token, ttl, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		s.save(ctx, token, ttl)
		return token, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (s *tokenStore) load(ctx context.Context) string {
	s.mu.RLock()
	if s.value != "" && time.Now().Before(s.expiresAt) {
		value := s.value
		s.mu.RUnlock()
		return value
	}
	s.mu.RUnlock()

	if s.cache == nil {
		return ""
	}
	var cached cachedToken
	if err := s.cache.Get(ctx, s.key, &cached); err != nil {
		return ""
	}
	if cached.Value == "" || time.Now().After(cached.ExpiresAt) {
		return ""
	}
	s.mu.Lock()
	s.value = cached.Value
	s.expiresAt = cached.ExpiresAt
	s.mu.Unlock()
	return cached.Value
}

func (s *tokenStore) save(ctx context.Context, value string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = time.Hour
	}
	lifetime := ttl - 5*time.Minute
	if lifetime < 30*time.Second {
		lifetime = ttl / 2
	}
	if lifetime <= 0 {
		lifetime = 30 * time.Second
	}
	expiresAt := time.Now().Add(lifetime)

	s.mu.Lock()
	s.value = value
	s.expiresAt = expiresAt
	s.mu.Unlock()

	if s.cache != nil {
		_ = s.cache.Set(ctx, s.key, cachedToken{Value: value, ExpiresAt: expiresAt}, lifetime)
	}
}

func (s *tokenStore) invalidate(ctx context.Context) {
	s.mu.Lock()
	s.value = ""
	s.expiresAt = time.Time{}
	s.mu.Unlock()
	if s.cache != nil {
		_ = s.cache.Delete(ctx, s.key)
	}
}

// AccessToken 返回应用 access_token，自动缓存并提前刷新。
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	return c.access.get(ctx, func(ctx context.Context) (string, time.Duration, error) {
		var response struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int64  `json:"expires_in"`
		}
		path := fmt.Sprintf("/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
			urlQueryEscape(c.app.AppID), urlQueryEscape(c.app.AppSecret))
		if err := c.getJSON(ctx, path, &response); err != nil {
			return "", 0, err
		}
		if response.AccessToken == "" {
			return "", 0, fmt.Errorf("weixin: empty access_token for app %q", c.app.Name)
		}
		return response.AccessToken, time.Duration(response.ExpiresIn) * time.Second, nil
	})
}

// withAccessToken 在 access_token 失效时自动刷新并重试一次。
func (c *Client) withAccessToken(ctx context.Context, fn func(token string) error) error {
	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	err = fn(token)
	if apiErr, ok := err.(*APIError); ok && apiErr.TokenInvalid() {
		c.access.invalidate(ctx)
		token, err = c.AccessToken(ctx)
		if err != nil {
			return err
		}
		return fn(token)
	}
	return err
}

// invalidateAccessToken 清除本地与共享缓存中的 access_token。
func (c *Client) invalidateAccessToken(ctx context.Context) {
	c.access.invalidate(ctx)
}
