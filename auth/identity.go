// Package auth 提供身份认证、JWT、密码哈希与会话管理。
package auth

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
)

// IdentityKey 是身份在 Gin Context 中的键。
const IdentityKey = "goframe.identity"

type identityContextKey struct{}

// Identity 是认证组件产生的身份，业务请求不得直接提交并覆盖其中的字段。
type Identity struct {
	UserID      string   `json:"user_id"`
	TenantID    string   `json:"tenant_id,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	AuthType    string   `json:"auth_type,omitempty"`
}

// Clone 返回深拷贝，避免调用方修改共享身份。
func (i *Identity) Clone() *Identity {
	if i == nil {
		return nil
	}
	clone := *i
	clone.Roles = append([]string(nil), i.Roles...)
	clone.Permissions = append([]string(nil), i.Permissions...)
	return &clone
}

// HasRole 判断是否拥有指定角色。
func (i *Identity) HasRole(role string) bool {
	for _, item := range i.Roles {
		if item == role {
			return true
		}
	}
	return false
}

// HasPermission 判断是否拥有指定权限，支持 * 与前缀通配。
func (i *Identity) HasPermission(action string) bool {
	return HasPermission(i.Permissions, action)
}

// HasPermission 判断权限列表中是否包含指定动作。
// 支持 user:*、* 等通配写法。
func HasPermission(permissions []string, action string) bool {
	for _, item := range permissions {
		if item == "*" || item == action {
			return true
		}
		if strings.HasSuffix(item, ":*") && strings.HasPrefix(action, strings.TrimSuffix(item, "*")) {
			return true
		}
	}
	return false
}

// WithIdentity 将身份写入 context。
func WithIdentity(ctx context.Context, identity *Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// FromContext 从 context 读取身份。
func FromContext(ctx context.Context) (*Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(*Identity)
	return identity, ok && identity != nil
}

// MustFromContext 从 context 读取身份，不存在时返回未认证错误。
func MustFromContext(ctx context.Context) (*Identity, error) {
	identity, ok := FromContext(ctx)
	if !ok {
		return nil, apperrors.Unauthorized("authentication required")
	}
	return identity, nil
}

// FromGin 从 Gin 请求上下文读取身份。
func FromGin(c *gin.Context) (*Identity, bool) {
	return FromContext(c.Request.Context())
}
