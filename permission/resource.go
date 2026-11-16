package permission

import (
	"net/http"

	apperrors "github.com/god-jason/goframe/errors"
)

// ResourceWideSuffix 是跨资源操作的权限后缀。
// 例如 user:update:any 表示可修改任意用户的资源。
const ResourceWideSuffix = ":any"

// CheckResource 校验租户隔离与资源归属。
// 跨租户访问一律拒绝；资源归属他人时需要资源级宽权限。
func CheckResource(subject Subject, permissions []string, action string, resource Resource, super bool) error {
	if super {
		return nil
	}
	if resource.TenantID != "" && subject.TenantID != "" && resource.TenantID != subject.TenantID {
		return apperrors.New(apperrors.CodeTenantDenied, http.StatusForbidden, "resource belongs to another tenant")
	}
	if resource.OwnerID != "" && subject.UserID != "" && resource.OwnerID != subject.UserID {
		if !HasPermission(permissions, action+ResourceWideSuffix) && !HasPermission(permissions, "*"+ResourceWideSuffix) {
			return apperrors.New(apperrors.CodePermissionDenied, http.StatusForbidden, "permission denied for this resource")
		}
	}
	return nil
}
