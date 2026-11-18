package api

import (
	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/permission"
)

// ResourceFunc 根据请求解析授权资源。
type ResourceFunc func(c *gin.Context) permission.Resource

// RequirePermission 要求当前身份拥有指定权限。
// 该中间件只是第一道防线，Service 层仍必须执行授权。
func RequirePermission(authorizer permission.Authorizer, action string, resourceFn ResourceFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.FromGin(c)
		if !ok {
			Fail(c, apperrors.Unauthorized("authentication required"))
			return
		}
		if authorizer == nil {
			Fail(c, apperrors.PermissionDenied())
			return
		}

		var resource permission.Resource
		if resourceFn != nil {
			resource = resourceFn(c)
		}
		if err := authorizer.Check(c.Request.Context(), permission.SubjectFromIdentity(identity), action, resource); err != nil {
			Fail(c, err)
			return
		}
		c.Next()
	}
}
