package upload

import (
	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/response"
)

// HTTPOptions 是上传接口配置。
type HTTPOptions struct {
	// Field 是 multipart 文件字段名，默认 "file"。
	Field string
	// DefaultCategory 是未传 category 时使用的分类，默认 "default"。
	DefaultCategory string
	// Tenant 用于覆盖租户解析逻辑；默认从认证身份读取 TenantID，其次 UserID。
	Tenant func(c *gin.Context) string
	// Category 用于覆盖分类解析逻辑；默认读取表单 category。
	Category func(c *gin.Context) string
}

// Handler 返回可直接挂载到 Gin 路由的上传处理器。
// 业务应在受保护分组中挂载，并按需配置权限中间件。
//
//	api.POST("/upload", application.Upload.Handler(upload.HTTPOptions{}))
func (s *Service) Handler(opts HTTPOptions) gin.HandlerFunc {
	field := opts.Field
	if field == "" {
		field = "file"
	}
	defaultCategory := opts.DefaultCategory
	if defaultCategory == "" {
		defaultCategory = "default"
	}

	return func(c *gin.Context) {
		fileHeader, err := c.FormFile(field)
		if err != nil {
			response.Fail(c, apperrors.Validation("file is required").WithCause(err))
			return
		}
		if fileHeader.Size > s.maxSize {
			response.Fail(c, apperrors.PayloadTooLarge())
			return
		}

		file, err := fileHeader.Open()
		if err != nil {
			response.Fail(c, apperrors.BadRequest("open uploaded file failed").WithCause(err))
			return
		}
		defer func() { _ = file.Close() }()

		tenantID := "default"
		if opts.Tenant != nil {
			tenantID = opts.Tenant(c)
		} else if identity, ok := auth.FromGin(c); ok {
			tenantID = identity.TenantID
			if tenantID == "" {
				tenantID = identity.UserID
			}
		}

		category := defaultCategory
		if opts.Category != nil {
			category = opts.Category(c)
		} else if value := c.PostForm("category"); value != "" {
			category = value
		}

		result, err := s.Save(c.Request.Context(), tenantID, category, fileHeader.Filename, file)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.Created(c, result)
	}
}
