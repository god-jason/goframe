package demo

import (
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/config"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/middleware"
	"github.com/god-jason/goframe/permission"
	"github.com/god-jason/goframe/response"
	"github.com/god-jason/goframe/security/audit"
	"github.com/god-jason/goframe/security/csrf"
	"github.com/god-jason/goframe/security/ratelimit"
)

type loginRequest struct {
	Username string `json:"username" binding:"required,max=64"`
	Password string `json:"password" binding:"required,max=128"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Handler 是示例 HTTP 处理器，只负责参数绑定、调用 Service 与组织响应。
type Handler struct {
	service      *Service
	auth         auth.Manager
	permission   permission.Authorizer
	csrf         *csrf.Protector
	auditor      audit.Auditor
	loginLimiter *ratelimit.KeyedLimiter
}

// NewHandler 创建示例处理器。
func NewHandler(service *Service, manager auth.Manager, authorizer permission.Authorizer) *Handler {
	return &Handler{
		service:      service,
		auth:         manager,
		permission:   authorizer,
		csrf:         csrf.Get(),
		auditor:      audit.Get(),
		loginLimiter: ratelimit.NewKeyedLimiter(5, 10),
	}
}

// RegisterRoutes 注册示例路由。
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	r.GET("/public/ping", h.ping)
	r.GET("/public/csrf-token", h.csrfToken)
	r.POST("/auth/login", middleware.RateLimit(h.loginLimiter, middleware.ClientIPKey, false), h.login)
	r.POST("/auth/refresh", h.refresh)

	protected := r.Group("", middleware.Authenticate(h.auth), middleware.RequireAuth())
	protected.POST("/auth/logout", h.logout)
	protected.GET("/user/me", h.me)
	protected.GET("/system/info",
		middleware.RequirePermission(h.permission, "system:read", nil),
		h.systemInfo,
	)
}

func (h *Handler) ping(c *gin.Context) {
	response.Success(c, gin.H{"message": "pong"})
}

func (h *Handler) csrfToken(c *gin.Context) {
	protector := h.csrf
	if protector == nil {
		response.Fail(c, apperrors.Unavailable("csrf protection is disabled"))
		return
	}
	token, err := protector.Token(c)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Success(c, gin.H{
		"token":  token,
		"header": protector.HeaderName(),
		"cookie": protector.CookieName(),
	})
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, apperrors.Validation("invalid request body"))
		return
	}
	if h.auth == nil {
		response.Fail(c, apperrors.Unavailable("authentication is disabled"))
		return
	}

	identity, err := h.service.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		h.record(c, "auth.login", "failure", "", map[string]any{"username": req.Username})
		response.Fail(c, err)
		return
	}

	tokens, identity, err := h.auth.Login(c.Request.Context(), identity, h.sessionMeta(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.record(c, "auth.login", "success", identity.UserID, map[string]any{"username": req.Username})
	response.Success(c, tokens)
}

func (h *Handler) refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, apperrors.Validation("invalid request body"))
		return
	}
	if h.auth == nil {
		response.Fail(c, apperrors.Unavailable("authentication is disabled"))
		return
	}

	tokens, _, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken, h.sessionMeta(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Success(c, tokens)
}

func (h *Handler) logout(c *gin.Context) {
	identity, _ := auth.FromGin(c)
	if h.auth == nil {
		response.NoContent(c)
		return
	}
	if err := h.auth.Logout(c.Request.Context(), identity.SessionID); err != nil {
		response.Fail(c, err)
		return
	}
	h.record(c, "auth.logout", "success", identity.UserID, nil)
	response.NoContent(c)
}

func (h *Handler) me(c *gin.Context) {
	identity, _ := auth.FromGin(c)
	response.Success(c, identity)
}

func (h *Handler) systemInfo(c *gin.Context) {
	app := config.App()
	response.Success(c, gin.H{
		"name":        app.Name,
		"version":     app.Version,
		"environment": app.Environment,
		"go_version":  runtime.Version(),
		"goroutines":  runtime.NumGoroutine(),
	})
}

func (h *Handler) sessionMeta(c *gin.Context) auth.SessionMeta {
	return auth.SessionMeta{
		DeviceName: c.GetHeader("X-Device-Name"),
		IP:         c.ClientIP(),
		UserAgent:  c.Request.UserAgent(),
	}
}

func (h *Handler) record(c *gin.Context, action, result, userID string, detail map[string]any) {
	if h.auditor == nil {
		return
	}
	event := audit.Event{
		ActorType: "user",
		ActorID:   userID,
		UserID:    userID,
		Action:    action,
		Result:    result,
		SourceIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		RequestID: response.RequestID(c),
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	}
	if err := h.auditor.Record(c.Request.Context(), event); err != nil {
		logger.Get().Warn("audit record failed")
	}
}
