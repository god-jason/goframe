package auth

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIdentityCloneAndChecks(t *testing.T) {
	identity := &Identity{
		UserID:      "u1",
		TenantID:    "t1",
		Roles:       []string{"admin"},
		Permissions: []string{"user:read", "device:*"},
	}

	if !identity.HasRole("admin") || identity.HasRole("viewer") {
		t.Fatal("HasRole returned unexpected result")
	}
	if !identity.HasPermission("user:read") || !identity.HasPermission("device:control") || identity.HasPermission("system:config") {
		t.Fatal("HasPermission returned unexpected result")
	}

	clone := identity.Clone()
	clone.Roles[0] = "changed"
	clone.Permissions[0] = "changed"
	if identity.Roles[0] != "admin" || identity.Permissions[0] != "user:read" {
		t.Fatal("Clone must deep copy slices")
	}

	var nilIdentity *Identity
	if nilIdentity.Clone() != nil {
		t.Fatal("cloning nil identity must return nil")
	}
}

func TestIdentityContext(t *testing.T) {
	identity := &Identity{UserID: "u1"}
	ctx := WithIdentity(context.Background(), identity)

	got, ok := FromContext(ctx)
	if !ok || got.UserID != "u1" {
		t.Fatalf("unexpected identity: %+v, %v", got, ok)
	}

	if _, err := MustFromContext(ctx); err != nil {
		t.Fatalf("MustFromContext: %v", err)
	}
	if _, err := MustFromContext(context.Background()); err == nil {
		t.Fatal("expected unauthenticated error")
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("expected no identity in empty context")
	}
}

func TestIdentityFromGin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithIdentity(req.Context(), &Identity{UserID: "u9"}))
	ginCtx.Request = req

	identity, ok := FromGin(ginCtx)
	if !ok || identity.UserID != "u9" {
		t.Fatalf("unexpected identity: %+v, %v", identity, ok)
	}
}
