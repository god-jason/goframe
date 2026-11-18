package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/id"
)

const (
	// APIKeyPrefix 是 API Key 的固定前缀。
	APIKeyPrefix = "gf_"
	// HeaderAPIKey 是 API Key 请求头。
	HeaderAPIKey = "X-API-Key"
	// apiKeyPrefixLen 是用于快速索引的明文前缀长度。
	apiKeyPrefixLen = len(APIKeyPrefix) + 8
)

// APIKey 是服务端保存的 API Key 记录，明文只在创建时返回一次。
type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Hash        string     `json:"-"`
	UserID      string     `json:"user_id"`
	TenantID    string     `json:"tenant_id,omitempty"`
	Roles       []string   `json:"roles,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// Clone 返回深拷贝。
func (k *APIKey) Clone() *APIKey {
	if k == nil {
		return nil
	}
	clone := *k
	clone.Roles = append([]string(nil), k.Roles...)
	clone.Permissions = append([]string(nil), k.Permissions...)
	if k.ExpiresAt != nil {
		t := *k.ExpiresAt
		clone.ExpiresAt = &t
	}
	if k.RevokedAt != nil {
		t := *k.RevokedAt
		clone.RevokedAt = &t
	}
	if k.LastUsedAt != nil {
		t := *k.LastUsedAt
		clone.LastUsedAt = &t
	}
	return &clone
}

// Valid 判断 API Key 是否仍然有效。
func (k *APIKey) Valid(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

// Identity 把 API Key 转换为认证身份。
func (k *APIKey) Identity() *Identity {
	return &Identity{
		UserID:      k.UserID,
		TenantID:    k.TenantID,
		Roles:       append([]string(nil), k.Roles...),
		Permissions: append([]string(nil), k.Permissions...),
		AuthType:    "api_key",
	}
}

// APIKeyRepository 是 API Key 的持久化接口。
type APIKeyRepository interface {
	Create(ctx context.Context, key *APIKey) error
	GetByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	UpdateLastUsed(ctx context.Context, id string, at time.Time) error
	Revoke(ctx context.Context, id string, at time.Time) error
	List(ctx context.Context, userID string) ([]*APIKey, error)
}

// NewAPIKey 生成 API Key。ttl<=0 表示永不过期。
// 返回的明文只在此处返回一次，服务端仅保存 sha256 哈希与前缀。
func NewAPIKey(name, userID, tenantID string, roles, permissions []string, ttl time.Duration) (string, *APIKey, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil, fmt.Errorf("auth: api key name is required")
	}
	if strings.TrimSpace(userID) == "" {
		return "", nil, fmt.Errorf("auth: api key user_id is required")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: generate api key: %w", err)
	}
	plain := APIKeyPrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	key := &APIKey{
		ID:          id.New(),
		Name:        name,
		Prefix:      plain[:apiKeyPrefixLen],
		Hash:        HashAPIKey(plain),
		UserID:      userID,
		TenantID:    tenantID,
		Roles:       append([]string(nil), roles...),
		Permissions: append([]string(nil), permissions...),
		CreatedAt:   time.Now(),
	}
	if ttl > 0 {
		expiresAt := key.CreatedAt.Add(ttl)
		key.ExpiresAt = &expiresAt
	}
	return plain, key, nil
}

// HashAPIKey 计算 API Key 的 sha256 哈希。
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifyAPIKey 校验明文 API Key 并返回身份。
// 校验失败统一返回同一个错误，避免泄露 Key 是否存在。
func VerifyAPIKey(ctx context.Context, plain string) (*Identity, error) {
	plain = strings.TrimSpace(plain)
	if len(plain) <= apiKeyPrefixLen || !strings.HasPrefix(plain, APIKeyPrefix) {
		return nil, apperrors.Unauthorized("invalid api key")
	}

	repository := global.apiKeys
	if repository == nil {
		return nil, apperrors.Unauthorized("api key authentication is not configured")
	}

	key, err := repository.GetByPrefix(ctx, plain[:apiKeyPrefixLen])
	if err != nil || key == nil {
		return nil, apperrors.Unauthorized("invalid api key")
	}
	if subtle.ConstantTimeCompare([]byte(key.Hash), []byte(HashAPIKey(plain))) != 1 {
		return nil, apperrors.Unauthorized("invalid api key")
	}
	if !key.Valid(time.Now()) {
		return nil, apperrors.Unauthorized("api key expired or revoked")
	}
	_ = repository.UpdateLastUsed(ctx, key.ID, time.Now())
	return key.Identity(), nil
}

// MemoryAPIKeyRepository 是进程内实现，仅适用于开发与测试。
type MemoryAPIKeyRepository struct {
	mu   sync.RWMutex
	keys map[string]*APIKey
}

// NewMemoryAPIKeyRepository 创建内存 API Key 仓库。
func NewMemoryAPIKeyRepository() *MemoryAPIKeyRepository {
	return &MemoryAPIKeyRepository{keys: make(map[string]*APIKey)}
}

// Create 保存 API Key。
func (r *MemoryAPIKeyRepository) Create(_ context.Context, key *APIKey) error {
	if key == nil || key.ID == "" {
		return fmt.Errorf("auth: api key is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[key.Prefix]; exists {
		return apperrors.Conflict("api key already exists")
	}
	r.keys[key.Prefix] = key.Clone()
	return nil
}

// GetByPrefix 按前缀读取 API Key。
func (r *MemoryAPIKeyRepository) GetByPrefix(_ context.Context, prefix string) (*APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[prefix]
	if !ok {
		return nil, apperrors.NotFound("api key not found")
	}
	return key.Clone(), nil
}

// UpdateLastUsed 更新最后使用时间。
func (r *MemoryAPIKeyRepository) UpdateLastUsed(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range r.keys {
		if key.ID == id {
			key.LastUsedAt = &at
			return nil
		}
	}
	return apperrors.NotFound("api key not found")
}

// Revoke 吊销 API Key。
func (r *MemoryAPIKeyRepository) Revoke(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range r.keys {
		if key.ID == id {
			key.RevokedAt = &at
			return nil
		}
	}
	return apperrors.NotFound("api key not found")
}

// List 列出用户的 API Key。
func (r *MemoryAPIKeyRepository) List(_ context.Context, userID string) ([]*APIKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var keys []*APIKey
	for _, key := range r.keys {
		if key.UserID == userID {
			keys = append(keys, key.Clone())
		}
	}
	return keys, nil
}
