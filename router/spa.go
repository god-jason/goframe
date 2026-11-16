package router

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/response"
)

// SPAOptions 是单页应用托管选项。
type SPAOptions struct {
	// Index 是入口文件，默认 index.html。
	Index string
	// ImmutablePrefix 是带内容 Hash 的静态资源前缀，命中后使用一年 immutable 缓存。
	ImmutablePrefix string
}

// NewSPAHandler 创建 SPA 静态资源处理器。
// API 未知路由返回 JSON 404，存在的静态文件直接返回，其余前端路由回退到 index.html。
func NewSPAHandler(fsys fs.FS, apiPath string, opts SPAOptions) (gin.HandlerFunc, error) {
	index := opts.Index
	if index == "" {
		index = "index.html"
	}
	indexContent, err := fs.ReadFile(fsys, index)
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(fsys))
	immutable := strings.Trim(opts.ImmutablePrefix, "/")

	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			response.Fail(c, apperrors.New(apperrors.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed"))
			return
		}

		requestPath := c.Request.URL.Path
		if isAPIPath(requestPath, apiPath) {
			response.Fail(c, apperrors.NotFound("route not found"))
			return
		}

		relative := strings.TrimPrefix(path.Clean(requestPath), "/")
		if relative == "" || relative == "." {
			relative = index
		}

		file, err := fsys.Open(relative)
		if err == nil {
			_ = file.Close()
			if immutable != "" && strings.HasPrefix(relative, immutable) {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexContent)
	}, nil
}

// DirectorySPA 使用外部目录托管 SPA。
func DirectorySPA(dir string, apiPath string, opts SPAOptions) (gin.HandlerFunc, error) {
	return NewSPAHandler(os.DirFS(dir), apiPath, opts)
}

func isAPIPath(requestPath, apiPath string) bool {
	if apiPath == "" {
		return false
	}
	return requestPath == apiPath || strings.HasPrefix(requestPath, apiPath+"/")
}
