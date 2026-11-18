package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/response"
)

// Authenticate 解析可选的身份令牌。令牌缺失时放行，令牌非法时返回 401。
// 支持 X-API-Key 请求头，以及 Authorization: Bearer <api-key>。
func Authenticate(authenticator auth.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey := strings.TrimSpace(c.GetHeader(auth.HeaderAPIKey)); apiKey != "" {
			identity, err := auth.VerifyAPIKey(c.Request.Context(), apiKey)
			if err != nil {
				response.Fail(c, err)
				return
			}
			setIdentity(c, identity)
			c.Next()
			return
		}

		token := bearerToken(c)
		if token == "" {
			c.Next()
			return
		}
		if strings.HasPrefix(token, auth.APIKeyPrefix) {
			identity, err := auth.VerifyAPIKey(c.Request.Context(), token)
			if err != nil {
				response.Fail(c, err)
				return
			}
			setIdentity(c, identity)
			c.Next()
			return
		}
		if authenticator == nil {
			c.Next()
			return
		}

		identity, err := authenticator.Authenticate(c.Request.Context(), token)
		if err != nil {
			response.Fail(c, err)
			return
		}
		setIdentity(c, identity)
		c.Next()
	}
}

// RequireAuth 要求请求已经完成认证。
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := auth.FromGin(c); !ok {
			response.Fail(c, apperrors.Unauthorized("authentication required"))
			return
		}
		c.Next()
	}
}

// HeaderTenant 是租户请求头。
const HeaderTenant = "X-Tenant-ID"

// TenantResolver 校验用户是否属于指定租户。业务模块实现该接口。
type TenantResolver interface {
	IsMember(ctx context.Context, userID, tenantID string) (bool, error)
}

// Tenant 解析当前租户并校验用户租户成员关系。
// 当前租户不能由客户端随意提交，必须经过成员关系验证。
func Tenant(resolver TenantResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := auth.FromGin(c)
		if !ok {
			c.Next()
			return
		}

		tenantID := strings.TrimSpace(c.GetHeader(HeaderTenant))
		if tenantID == "" {
			tenantID = identity.TenantID
		}
		if tenantID == "" || tenantID == identity.TenantID {
			c.Next()
			return
		}

		if resolver == nil {
			response.Fail(c, apperrors.New(apperrors.CodeTenantDenied, http.StatusForbidden, "tenant access denied"))
			return
		}
		member, err := resolver.IsMember(c.Request.Context(), identity.UserID, tenantID)
		if err != nil {
			response.Fail(c, err)
			return
		}
		if !member {
			response.Fail(c, apperrors.New(apperrors.CodeTenantDenied, http.StatusForbidden, "tenant access denied"))
			return
		}

		clone := identity.Clone()
		clone.TenantID = tenantID
		setIdentity(c, clone)
		c.Next()
	}
}

func bearerToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func setIdentity(c *gin.Context, identity *auth.Identity) {
	c.Set(auth.IdentityKey, identity)
	c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), identity))
}
