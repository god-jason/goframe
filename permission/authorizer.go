// Package permission 提供 RBAC + 资源授权能力。
package permission

import (
	"context"
	"strings"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

// Subject 是授权主体。
type Subject struct {
	UserID      string            `json:"user_id"`
	TenantID    string            `json:"tenant_id,omitempty"`
	Roles       []string          `json:"roles,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// Resource 是授权资源。
type Resource struct {
	Type       string            `json:"type,omitempty"`
	ID         string            `json:"id,omitempty"`
	TenantID   string            `json:"tenant_id,omitempty"`
	OwnerID    string            `json:"owner_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Authorizer 是框架统一授权接口。授权必须在 Service 层执行，
// 不能只依赖 Gin Middleware。
type Authorizer interface {
	Check(ctx context.Context, subject Subject, action string, resource Resource) error
}

// SubjectFromIdentity 将认证身份转换为授权主体。
func SubjectFromIdentity(identity *auth.Identity) Subject {
	if identity == nil {
		return Subject{}
	}
	return Subject{
		UserID:      identity.UserID,
		TenantID:    identity.TenantID,
		Roles:       append([]string(nil), identity.Roles...),
		Permissions: append([]string(nil), identity.Permissions...),
	}
}

// HasPermission 判断权限列表是否包含指定动作，支持 * 与前缀通配。
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

// Authorize 是一次授权检查的便捷封装。
func Authorize(ctx context.Context, authorizer Authorizer, identity *auth.Identity, action string, resource Resource) error {
	if authorizer == nil {
		return apperrors.PermissionDenied()
	}
	return authorizer.Check(ctx, SubjectFromIdentity(identity), action, resource)
}
