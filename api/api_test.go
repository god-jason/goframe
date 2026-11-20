package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func guardMarker(order *[]string, name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		*order = append(*order, name)
		c.Next()
	}
}

func marker(order *[]string, name string, code int) gin.HandlerFunc {
	return func(c *gin.Context) {
		*order = append(*order, name)
		c.Status(code)
	}
}

func serve(engine *gin.Engine, method, path string) int {
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code
}

// TestRegisterRootAdminAppliesAdminGuard 验证框架管理员根接口会附加管理员守卫。
func TestRegisterRootAdminAppliesAdminGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Reset()
	defer Reset()

	var order []string
	RegisterRootAdmin(http.MethodGet, "/system", marker(&order, "system", http.StatusOK))

	engine := gin.New()
	if err := MountRoot(engine, WithGuard(Admin, guardMarker(&order, "admin-guard"))); err != nil {
		t.Fatalf("MountRoot: %v", err)
	}
	if code := serve(engine, http.MethodGet, "/system"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if strings.Join(order, ",") != "admin-guard,system" {
		t.Fatalf("order = %v", order)
	}
}

func TestMountAppliesGuardsPerAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Reset()
	defer Reset()

	var order []string
	RegisterPublic("GET", "/ping", marker(&order, "public", http.StatusOK))
	Register("GET", "/me", marker(&order, "login", http.StatusOK))
	RegisterAdmin("post", "/admin/do", marker(&order, "admin", http.StatusCreated))

	engine := gin.New()
	group := engine.Group("/api")
	err := Mount(group,
		WithGuard(Login, guardMarker(&order, "login-guard")),
		WithGuard(Admin, guardMarker(&order, "admin-guard")),
	)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	if code := serve(engine, http.MethodGet, "/api/ping"); code != http.StatusOK {
		t.Fatalf("public status = %d", code)
	}
	if code := serve(engine, http.MethodGet, "/api/me"); code != http.StatusOK {
		t.Fatalf("login status = %d", code)
	}
	if code := serve(engine, http.MethodPost, "/api/admin/do"); code != http.StatusCreated {
		t.Fatalf("admin status = %d", code)
	}

	want := []string{"public", "login-guard", "login", "admin-guard", "admin"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestMountRejectsInvalidEntries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	noop := func(c *gin.Context) { c.Status(http.StatusOK) }

	tests := []struct {
		name     string
		register func()
	}{
		{"invalid method", func() { Register("FOO", "/x", noop) }},
		{"invalid path", func() { RegisterPublic("GET", "x", noop) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Reset()
			defer Reset()
			tt.register()

			engine := gin.New()
			if err := Mount(engine.Group("/api")); err == nil {
				t.Fatal("expected Mount error")
			}
			if len(engine.Routes()) != 0 {
				t.Fatalf("expected no routes mounted, got %d", len(engine.Routes()))
			}
		})
	}
}

func TestReRegisterOverwrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Reset()
	defer Reset()

	engine := gin.New()
	RegisterPublic("GET", "/dup", func(c *gin.Context) { c.Status(http.StatusTeapot) })
	Register("GET", "/dup", func(c *gin.Context) { c.Status(http.StatusOK) })
	if len(Entries()) != 1 {
		t.Fatalf("entries = %d, want 1", len(Entries()))
	}
	if err := Mount(engine.Group("/api"), WithGuard(Login)); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if code := serve(engine, http.MethodGet, "/api/dup"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (last registration wins)", code)
	}
}

func TestEntriesAndReset(t *testing.T) {
	Reset()
	defer Reset()

	if len(Entries()) != 0 {
		t.Fatal("expected empty registry")
	}
	Register("GET", "/x", func(c *gin.Context) {})
	snapshot := Entries()
	if len(snapshot) != 1 || snapshot[0].Method != "GET" || snapshot[0].Access != Login {
		t.Fatalf("unexpected entries: %+v", snapshot)
	}
	Reset()
	if len(Entries()) != 0 {
		t.Fatal("Reset did not clear registry")
	}
}
