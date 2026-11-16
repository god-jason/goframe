package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/permission"
)

type stubAuthenticator struct {
	identity *auth.Identity
	err      error
}

func (s stubAuthenticator) Authenticate(context.Context, string) (*auth.Identity, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.identity == nil {
		return nil, apperrors.Unauthorized("invalid token")
	}
	return s.identity.Clone(), nil
}

type stubAuthorizer struct {
	err      error
	action   string
	resource permission.Resource
	subject  permission.Subject
}

func (s *stubAuthorizer) Check(_ context.Context, subject permission.Subject, action string, resource permission.Resource) error {
	s.subject = subject
	s.action = action
	s.resource = resource
	return s.err
}

type stubTenantResolver struct {
	member bool
	err    error
	calls  int
	lastID string
}

func (r *stubTenantResolver) IsMember(_ context.Context, _ string, tenantID string) (bool, error) {
	r.calls++
	r.lastID = tenantID
	return r.member, r.err
}

func identityHandler(c *gin.Context) {
	identity, ok := auth.FromGin(c)
	if !ok {
		c.String(http.StatusOK, "anonymous")
		return
	}
	c.String(http.StatusOK, identity.UserID)
}

func TestAuthenticateMiddleware(t *testing.T) {
	good := stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}
	engine := newTestEngine(Authenticate(good))
	engine.GET("/who", identityHandler)

	if recorder := doRequest(engine, http.MethodGet, "/who", nil, nil); recorder.Body.String() != "anonymous" {
		t.Fatalf("body = %q, want anonymous", recorder.Body.String())
	}
	if recorder := doRequest(engine, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer token"}); recorder.Body.String() != "u1" {
		t.Fatalf("body = %q, want u1", recorder.Body.String())
	}

	bad := newTestEngine(Authenticate(stubAuthenticator{err: apperrors.Unauthorized("invalid token")}))
	bad.GET("/who", identityHandler)
	recorder := doRequest(bad, http.MethodGet, "/who", nil, map[string]string{"Authorization": "Bearer bad"})
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	engine := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}),
		RequireAuth(),
	)
	engine.GET("/private", identityHandler)

	recorder := doRequest(engine, http.MethodGet, "/private", nil, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}

	recorder = doRequest(engine, http.MethodGet, "/private", nil, map[string]string{"Authorization": "Bearer token"})
	if recorder.Code != http.StatusOK || recorder.Body.String() != "u1" {
		t.Fatalf("unexpected response: %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestRequirePermissionMiddleware(t *testing.T) {
	authorizer := &stubAuthorizer{}
	engine := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}),
		RequirePermission(authorizer, "user:read", func(*gin.Context) permission.Resource {
			return permission.Resource{Type: "user", ID: "42", TenantID: "t1"}
		}),
	)
	engine.GET("/users/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := doRequest(engine, http.MethodGet, "/users/42", nil, map[string]string{"Authorization": "Bearer token"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if authorizer.action != "user:read" || authorizer.resource.ID != "42" || authorizer.subject.UserID != "u1" {
		t.Fatalf("unexpected authorization call: %+v", authorizer)
	}

	denied := &stubAuthorizer{err: apperrors.PermissionDenied()}
	engine2 := newTestEngine(
		Authenticate(stubAuthenticator{identity: &auth.Identity{UserID: "u1"}}),
		RequirePermission(denied, "user:read", nil),
	)
	engine2.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder = doRequest(engine2, http.MethodGet, "/users", nil, map[string]string{"Authorization": "Bearer token"})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}

	unauthenticated := newTestEngine(RequirePermission(authorizer, "user:read", nil))
	unauthenticated.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })
	if recorder := doRequest(unauthenticated, http.MethodGet, "/users", nil, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestTenantMiddleware(t *testing.T) {
	identity := &auth.Identity{UserID: "u1", TenantID: "t1"}
	resolver := &stubTenantResolver{member: true}
	engine := newTestEngine(
		Authenticate(stubAuthenticator{identity: identity}),
		Tenant(resolver),
	)
	engine.GET("/tenant", func(c *gin.Context) {
		current, _ := auth.FromGin(c)
		c.String(http.StatusOK, current.TenantID)
	})

	if recorder := doRequest(engine, http.MethodGet, "/tenant", nil, map[string]string{"Authorization": "Bearer token"}); recorder.Body.String() != "t1" {
		t.Fatalf("body = %q, want t1", recorder.Body.String())
	}

	recorder := doRequest(engine, http.MethodGet, "/tenant", nil, map[string]string{
		"Authorization": "Bearer token",
		HeaderTenant:    "t2",
	})
	if recorder.Code != http.StatusOK || recorder.Body.String() != "t2" {
		t.Fatalf("unexpected response: %d %q", recorder.Code, recorder.Body.String())
	}
	if resolver.calls != 1 || resolver.lastID != "t2" {
		t.Fatalf("resolver calls = %d, last = %q", resolver.calls, resolver.lastID)
	}

	resolver.member = false
	recorder = doRequest(engine, http.MethodGet, "/tenant", nil, map[string]string{
		"Authorization": "Bearer token",
		HeaderTenant:    "t3",
	})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}

	noResolver := newTestEngine(
		Authenticate(stubAuthenticator{identity: identity}),
		Tenant(nil),
	)
	noResolver.GET("/tenant", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder = doRequest(noResolver, http.MethodGet, "/tenant", nil, map[string]string{
		"Authorization": "Bearer token",
		HeaderTenant:    "t4",
	})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}
