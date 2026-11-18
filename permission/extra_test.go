package permission

import (
	"context"
	"testing"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

func TestGlobalAuthorizer(t *testing.T) {
	original := Get()
	t.Cleanup(func() { Set(nil) })

	if original == nil {
		t.Fatal("default authorizer must not be nil")
	}
	if Get() == nil {
		t.Fatal("Get must return the default authorizer")
	}

	custom := NewRBAC(Policy{Roles: map[string][]string{"op": {"device:read"}}})
	Set(custom)
	if got := Get(); got != custom {
		t.Fatal("Set must replace the global authorizer")
	}
	if err := Get().Check(context.Background(), Subject{Roles: []string{"op"}}, "device:read", Resource{}); err != nil {
		t.Fatalf("custom authorizer check: %v", err)
	}

	// nil 应恢复默认策略；默认策略包含 super_admin 超级角色。
	Set(nil)
	if err := Get().Check(context.Background(), Subject{Roles: []string{"super_admin"}}, "anything", Resource{}); err != nil {
		t.Fatalf("reset authorizer should allow super role: %v", err)
	}
}

func TestRBACGrantDeduplicates(t *testing.T) {
	rbac := NewRBAC(Policy{Roles: map[string][]string{"user": {"user:read"}}})
	rbac.Grant("user", "user:read", "user:update", "user:update")

	permissions := rbac.Permissions([]string{"user"})
	want := []string{"user:read", "user:update"}
	if len(permissions) != len(want) {
		t.Fatalf("permissions = %v, want %v", permissions, want)
	}
	for i := range want {
		if permissions[i] != want[i] {
			t.Fatalf("permissions = %v, want %v", permissions, want)
		}
	}
}

func TestRBACPermissionsMergesRolesWithoutDuplicates(t *testing.T) {
	rbac := NewRBAC(Policy{Roles: map[string][]string{
		"viewer": {"user:read"},
		"editor": {"user:read", "user:update"},
	}})
	permissions := rbac.Permissions([]string{"viewer", "editor", "unknown"})
	want := []string{"user:read", "user:update"}
	if len(permissions) != len(want) {
		t.Fatalf("permissions = %v, want %v", permissions, want)
	}
	for i := range want {
		if permissions[i] != want[i] {
			t.Fatalf("permissions = %v, want %v", permissions, want)
		}
	}
}

func TestRBACAddSuperRole(t *testing.T) {
	rbac := NewRBAC(Policy{})
	rbac.AddSuperRole("root")
	if err := rbac.Check(context.Background(), Subject{Roles: []string{"root"}}, "system:anything", Resource{TenantID: "other"}); err != nil {
		t.Fatalf("super role should bypass checks: %v", err)
	}
}

func TestRBACRejectsEmptyAction(t *testing.T) {
	rbac := NewRBAC(DefaultPolicy())
	err := rbac.Check(context.Background(), Subject{Permissions: []string{"*"}}, "", Resource{})
	if got := apperrors.From(err); got.Code != apperrors.CodePermissionDenied {
		t.Fatalf("code = %d, want %d", got.Code, apperrors.CodePermissionDenied)
	}
}

func TestCheckResourceEdgeCases(t *testing.T) {
	// 租户或主体任一为空时不做跨租户判定。
	if err := CheckResource(Subject{UserID: "u1"}, nil, "device:read", Resource{TenantID: "t1"}, false); err != nil {
		t.Fatalf("empty subject tenant should be allowed: %v", err)
	}
	if err := CheckResource(Subject{UserID: "u1", TenantID: "t1"}, nil, "device:read", Resource{}, false); err != nil {
		t.Fatalf("empty resource tenant should be allowed: %v", err)
	}
	// 资源无归属或主体无 UserID 时不校验归属。
	if err := CheckResource(Subject{UserID: "u1", TenantID: "t1"}, nil, "device:read", Resource{}, false); err != nil {
		t.Fatalf("resource without owner should be allowed: %v", err)
	}
	if err := CheckResource(Subject{TenantID: "t1"}, nil, "device:read", Resource{OwnerID: "u2"}, false); err != nil {
		t.Fatalf("subject without user id should be allowed: %v", err)
	}

	// *:any 表示对任意资源生效。
	subject := Subject{UserID: "u1", TenantID: "t1"}
	others := Resource{TenantID: "t1", OwnerID: "u2"}
	if err := CheckResource(subject, []string{"*:any"}, "device:control", others, false); err != nil {
		t.Fatalf("wildcard resource-wide permission should be allowed: %v", err)
	}

	if err := CheckResource(subject, nil, "device:control", others, false); err == nil {
		t.Fatal("expected resource ownership denial")
	}
	if err := CheckResource(subject, nil, "device:control", others, true); err != nil {
		t.Fatalf("super should bypass resource checks: %v", err)
	}
}

func TestAuthorizeWithNilIdentity(t *testing.T) {
	rbac := NewRBAC(DefaultPolicy())
	if err := Authorize(context.Background(), rbac, nil, "system:read", Resource{}); err == nil {
		t.Fatal("expected anonymous subject to be denied")
	}
	if err := Authorize(context.Background(), rbac, &auth.Identity{UserID: "u1", Permissions: []string{"*"}}, "system:read", Resource{}); err != nil {
		t.Fatalf("expected wildcard identity to be allowed: %v", err)
	}
}
