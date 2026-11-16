package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRouterGroupsAndPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router := New(engine, Options{APIPrefix: "/api", APIVersion: "v1"})

	router.Public().GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "public") })
	router.Auth().POST("/login", func(c *gin.Context) { c.String(http.StatusOK, "auth") })
	router.User().GET("", func(c *gin.Context) { c.String(http.StatusOK, "user") })
	router.Admin().GET("/dashboard", func(c *gin.Context) { c.String(http.StatusOK, "admin") })
	router.Tenant().GET("/info", func(c *gin.Context) { c.String(http.StatusOK, "tenant") })
	router.System().GET("/info", func(c *gin.Context) { c.String(http.StatusOK, "system") })
	router.Group("/custom").GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "custom") })

	cases := []struct {
		method string
		target string
		want   string
	}{
		{http.MethodGet, "/api/v1/public/ping", "public"},
		{http.MethodPost, "/api/v1/auth/login", "auth"},
		{http.MethodGet, "/api/v1/user", "user"},
		{http.MethodGet, "/api/v1/admin/dashboard", "admin"},
		{http.MethodGet, "/api/v1/tenant/info", "tenant"},
		{http.MethodGet, "/api/v1/system/info", "system"},
		{http.MethodGet, "/api/v1/custom/x", "custom"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK || recorder.Body.String() != tc.want {
			t.Fatalf("%s %s = %d %q, want 200 %q", tc.method, tc.target, recorder.Code, recorder.Body.String(), tc.want)
		}
	}
}

func TestRouterWithoutVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router := New(engine, Options{APIPrefix: "/api"})
	router.Public().GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/ping", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}
