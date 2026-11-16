package permission

import (
	"context"
	"sync"

	apperrors "github.com/god-jason/goframe/errors"
)

// RBAC 是 RBAC + 租户/资源授权实现。
type RBAC struct {
	mu         sync.RWMutex
	rolePerms  map[string][]string
	superRoles map[string]struct{}
}

// NewRBAC 创建授权器。
func NewRBAC(policy Policy) *RBAC {
	r := &RBAC{
		rolePerms:  make(map[string][]string),
		superRoles: make(map[string]struct{}),
	}
	for role, permissions := range policy.Roles {
		r.rolePerms[role] = append([]string(nil), permissions...)
	}
	for _, role := range policy.SuperRoles {
		r.superRoles[role] = struct{}{}
	}
	return r
}

// Grant 为角色授予权限。
func (r *RBAC) Grant(role string, permissions ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := r.rolePerms[role]
	for _, permission := range permissions {
		exists := false
		for _, item := range existing {
			if item == permission {
				exists = true
				break
			}
		}
		if !exists {
			existing = append(existing, permission)
		}
	}
	r.rolePerms[role] = existing
}

// Permissions 返回角色集合拥有的权限。
func (r *RBAC) Permissions(roles []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var permissions []string
	seen := make(map[string]struct{})
	for _, role := range roles {
		for _, permission := range r.rolePerms[role] {
			if _, ok := seen[permission]; ok {
				continue
			}
			seen[permission] = struct{}{}
			permissions = append(permissions, permission)
		}
	}
	return permissions
}

// AddSuperRole 新增超级管理员角色。
func (r *RBAC) AddSuperRole(role string) {
	r.mu.Lock()
	r.superRoles[role] = struct{}{}
	r.mu.Unlock()
}

// Check 执行授权检查：先校验权限，再校验租户与资源归属。
func (r *RBAC) Check(ctx context.Context, subject Subject, action string, resource Resource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action == "" {
		return apperrors.PermissionDenied()
	}

	super := r.isSuper(subject.Roles)
	permissions := append([]string(nil), subject.Permissions...)
	permissions = append(permissions, r.Permissions(subject.Roles)...)

	if !super && !HasPermission(permissions, action) {
		return apperrors.PermissionDenied()
	}
	if err := CheckResource(subject, permissions, action, resource, super); err != nil {
		return err
	}
	return nil
}

func (r *RBAC) isSuper(roles []string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, role := range roles {
		if _, ok := r.superRoles[role]; ok {
			return true
		}
	}
	return false
}
