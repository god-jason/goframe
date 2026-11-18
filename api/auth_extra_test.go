package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
)

func withIdentity(identity *auth.Identity) gin.HandlerFunc {
	return func(c *gin.Context) {
		setIdentity(c, identity)
		c.Next()
	}
}

func TestAuthenticateAPIKeys(t *testing.T) {
	plain, key, err := auth.NewAPIKey("tests", "u1", "t1", nil, nil, 0)
	if err != nil {
		t.Fatalf("new api key: %v", err)
	}
	repository := auth.NewMemoryAPIKeyRepository()
	if err := repository.Create(context.Background(), key); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	auth.SetAPIKeyRepository(repository)
	t.Cleanup(func() { auth.SetAPIKeyRepository(nil) })

	engine := newTestEngine(Authenticate(nil))
	engine.GET("/who", identityHandler)

	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{auth.HeaderAPIKey: plain}); recorder.Body.String() != "u1" {
		t.Fatalf("api key header body = %q", recorder.Body.String())
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer " + plain}); recorder.Body.String() != "u1" {
		t.Fatalf("bearer api key body = %q", recorder.Body.String())
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{auth.HeaderAPIKey: "invalid"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid api key header status = %d", recorder.Code)
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer gf_invalid"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer api key status = %d", recorder.Code)
	}
	// 非 API Key 令牌且未配置认证器时匿名放行。
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer opaque"}); recorder.Body.String() != "anonymous" {
		t.Fatalf("nil authenticator body = %q", recorder.Body.String())
	}
}

func TestAuthenticateMalformedAuthorization(t *testing.T) {
	engine := newTestEngine(Authenticate(nil))
	engine.GET("/who", identityHandler)

	for _, header := range []string{"Basic abc", "Bearer", "Bearer   "} {
		if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": header}); recorder.Body.String() != "anonymous" {
			t.Fatalf("header %q body = %q, want anonymous", header, recorder.Body.String())
		}
	}
}

func TestTenantEdgeCases(t *testing.T) {
	resolver := &stubTenantResolver{member: true}

	anonymous := newTestEngine(Tenant(resolver))
	anonymous.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(anonymous, http.MethodGet, "/tenant", nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d", recorder.Code)
	}

	noTenant := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}),
		Tenant(nil),
	)
	noTenant.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(noTenant, http.MethodGet, "/tenant", nil, map[string]string{"Authorization": "Bearer token"}); recorder.Code != http.StatusOK {
		t.Fatalf("missing tenant status = %d", recorder.Code)
	}

	failing := &stubTenantResolver{err: errors.New("resolver down")}
	failingEngine := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1", TenantID: "t1"}}),
		Tenant(failing),
	)
	failingEngine.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := doRequest(failingEngine, http.MethodGet, "/tenant", nil, map[string]string{
		"Authorization": "Bearer token",
		HeaderTenant:    "t2",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("resolver failure status = %d", recorder.Code)
	}
}

func TestRequirePermissionNilAuthorizer(t *testing.T) {
	engine := newTestEngine(
		withIdentity(&auth.Identity{UserID: "u1"}),
		RequirePermission(nil, "user:read", nil),
	)
	engine.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })

	if recorder := doRequest(engine, http.MethodGet, "/users", nil, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}
