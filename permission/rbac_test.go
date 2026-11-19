package permission

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

func TestHasPermission(t *testing.T) {
	cases := []struct {
		permissions []string
		action      string
		want        bool
	}{
		{[]string{"user:read"}, "user:read", true},
		{[]string{"user:read"}, "user:update", false},
		{[]string{"user:*"}, "user:update", true},
		{[]string{"user:*"}, "device:read", false},
		{[]string{"*"}, "anything", true},
		{nil, "user:read", false},
	}
	for _, tc := range cases {
		if got := auth.HasPermission(tc.permissions, tc.action); got != tc.want {
			t.Fatalf("HasPermission(%v, %q) = %v, want %v", tc.permissions, tc.action, got, tc.want)
		}
	}
}

func TestRBACAllowsRolePermissions(t *testing.T) {
	rbac := NewRBAC(Policy{
		Roles: map[string][]string{
			"viewer": {"user:read"},
			"editor": {"user:*"},
		},
	})

	viewer := Subject{UserID: "u1", Roles: []string{"viewer"}}
	if err := rbac.Check(context.Background(), viewer, "user:read", Resource{}); err != nil {
		t.Fatalf("expected user:read to be allowed: %v", err)
	}
	if err := rbac.Check(context.Background(), viewer, "user:update", Resource{}); err == nil {
		t.Fatal("expected user:update to be denied")
	}

	editor := Subject{UserID: "u2", Roles: []string{"editor"}}
	if err := rbac.Check(context.Background(), editor, "user:update", Resource{}); err != nil {
		t.Fatalf("expected wildcard permission to allow update: %v", err)
	}
}

func TestRBACDirectPermissionsAndSuperRole(t *testing.T) {
	rbac := NewRBAC(DefaultPolicy())

	subject := Subject{UserID: "u1"}
	if err := rbac.Check(context.Background(), subject, "device:control", Resource{}); err == nil {
		t.Fatal("expected missing permission to be denied")
	}

	subject.Permissions = []string{"device:control"}
	if err := rbac.Check(context.Background(), subject, "device:control", Resource{}); err != nil {
		t.Fatalf("expected direct permission to be allowed: %v", err)
	}

	super := Subject{UserID: "admin", Roles: []string{"super_admin"}}
	if err := rbac.Check(context.Background(), super, "anything:any", Resource{TenantID: "other"}); err != nil {
		t.Fatalf("expected super role to bypass checks: %v", err)
	}
}

func TestRBACTenantIsolation(t *testing.T) {
	rbac := NewRBAC(Policy{Roles: map[string][]string{"user": {"device:read"}}})
	subject := Subject{UserID: "u1", TenantID: "tenant-a", Roles: []string{"user"}}

	err := rbac.Check(context.Background(), subject, "device:read", Resource{Type: "device", ID: "d1", TenantID: "tenant-b"})
	if err == nil {
		t.Fatal("expected cross-tenant access to be denied")
	}
	if got := apperrors.From(err); got.Code != apperrors.CodeTenantDenied {
		t.Fatalf("code = %d, want %d", got.Code, apperrors.CodeTenantDenied)
	}
}

func TestRBACResourceOwnership(t *testing.T) {
	rbac := NewRBAC(Policy{Roles: map[string][]string{"user": {"device:control"}}})

	subject := Subject{UserID: "u1", TenantID: "t1", Roles: []string{"user"}}
	owned := Resource{Type: "device", ID: "d1", TenantID: "t1", OwnerID: "u1"}
	if err := rbac.Check(context.Background(), subject, "device:control", owned); err != nil {
		t.Fatalf("expected owned resource to be allowed: %v", err)
	}

	others := Resource{Type: "device", ID: "d2", TenantID: "t1", OwnerID: "u2"}
	if err := rbac.Check(context.Background(), subject, "device:control", others); err == nil {
		t.Fatal("expected other user's resource to be denied")
	}

	rbac.Grant("user", "device:control:any")
	if err := rbac.Check(context.Background(), subject, "device:control", others); err != nil {
		t.Fatalf("expected resource-wide permission to be allowed: %v", err)
	}
}

func TestRBACContextCanceled(t *testing.T) {
	rbac := NewRBAC(DefaultPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rbac.Check(ctx, Subject{Permissions: []string{"*"}}, "user:read", Resource{})
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestSubjectFromIdentity(t *testing.T) {
	identity := &auth.Identity{
		UserID:      "u1",
		TenantID:    "t1",
		Roles:       []string{"admin"},
		Permissions: []string{"user:read"},
	}
	subject := SubjectFromIdentity(identity)
	if subject.UserID != "u1" || subject.TenantID != "t1" {
		t.Fatalf("unexpected subject: %+v", subject)
	}
	identity.Roles[0] = "changed"
	if subject.Roles[0] != "admin" {
		t.Fatal("SubjectFromIdentity must copy slices")
	}

	if empty := SubjectFromIdentity(nil); empty.UserID != "" {
		t.Fatalf("expected zero subject, got %+v", empty)
	}
}

func TestAuthorizeHelper(t *testing.T) {
	rbac := NewRBAC(Policy{Roles: map[string][]string{"user": {"user:read"}}})
	identity := &auth.Identity{UserID: "u1", Roles: []string{"user"}}

	if err := Authorize(context.Background(), rbac, identity, "user:read", Resource{}); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if err := Authorize(context.Background(), nil, identity, "user:read", Resource{}); err == nil {
		t.Fatal("expected nil authorizer to deny")
	}
}
