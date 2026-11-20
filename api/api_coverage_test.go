package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterRootAndMountRoot 覆盖 RegisterRoot 与 MountRoot 的函数体。
func TestRegisterRootAndMountRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Reset()
	defer Reset()

	var hit bool
	RegisterRoot(http.MethodGet, "/metrics", func(c *gin.Context) {
		hit = true
		c.Status(http.StatusOK)
	})

	entries := Entries()
	if len(entries) != 1 || !entries[0].Root {
		t.Fatalf("entries = %+v", entries)
	}

	engine := gin.New()
	if err := MountRoot(engine); err != nil {
		t.Fatalf("MountRoot: %v", err)
	}
	if code := serve(engine, http.MethodGet, "/metrics"); code != http.StatusOK || !hit {
		t.Fatalf("status = %d, hit = %v", code, hit)
	}
}

// TestNormalizePathBody 覆盖 NormalizePath 函数体。
func TestNormalizePathBody(t *testing.T) {
	if got := NormalizePath("/a/b/"); got != "/a/b" {
		t.Fatalf("NormalizePath = %q", got)
	}
	if got := NormalizePath(""); got != "/" {
		t.Fatalf("NormalizePath empty = %q", got)
	}
}

// TestSPARootPathFallsBackToIndex 覆盖 SPA 处理器中请求根路径回退到 index 的分支。
func TestSPARootPathFallsBackToIndex(t *testing.T) {
	engine := newSPAEngine(t)
	recorder := performSPA(engine, http.MethodGet, "/")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}
