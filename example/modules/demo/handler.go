package demo

import (
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/config"
	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/logger"
	"github.com/god-jason/goframe/permission"
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
	registered   bool
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

// RegisterAPIs 把示例接口登记到全局 api 注册表。
func (h *Handler) RegisterAPIs() {
	api.RegisterPublic("GET", "/public/ping", h.ping)
	api.RegisterPublic("GET", "/public/csrf-token", h.csrfToken)
	api.RegisterPublic("POST", "/auth/login", ratelimit.Middleware(h.loginLimiter, ratelimit.ClientIPKey, false), h.login)
	api.RegisterPublic("POST", "/auth/refresh", h.refresh)
	api.Register("POST", "/auth/logout", h.logout)
	api.Register("GET", "/user/me", h.me)
	api.Register("GET", "/system/info", api.RequirePermission(h.permission, "system:read", nil), h.systemInfo)
	h.registered = true
}

// Mount 把已登记接口统一挂载到 group，登录守卫使用本处理器的认证器。
func (h *Handler) Mount(group gin.IRoutes) error {
	if !h.registered {
		h.RegisterAPIs()
	}
	return api.Mount(group, api.WithGuard(api.Login,
		api.Authenticate(h.auth),
		api.RequireAuth(),
	))
}

func (h *Handler) ping(c *gin.Context) {
	api.Success(c, gin.H{"message": "pong"})
}

func (h *Handler) csrfToken(c *gin.Context) {
	protector := h.csrf
	if protector == nil {
		api.Fail(c, apperrors.Unavailable("csrf protection is disabled"))
		return
	}
	token, err := protector.Token(c)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Success(c, gin.H{
		"token":  token,
		"header": protector.HeaderName(),
		"cookie": protector.CookieName(),
	})
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Fail(c, apperrors.Validation("invalid request body"))
		return
	}
	if h.auth == nil {
		api.Fail(c, apperrors.Unavailable("authentication is disabled"))
		return
	}

	identity, err := h.service.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		h.record(c, "auth.login", "failure", "", map[string]any{"username": req.Username})
		api.Fail(c, err)
		return
	}

	tokens, identity, err := h.auth.Login(c.Request.Context(), identity, h.sessionMeta(c))
	if err != nil {
		api.Fail(c, err)
		return
	}
	h.record(c, "auth.login", "success", identity.UserID, map[string]any{"username": req.Username})
	api.Success(c, tokens)
}

func (h *Handler) refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Fail(c, apperrors.Validation("invalid request body"))
		return
	}
	if h.auth == nil {
		api.Fail(c, apperrors.Unavailable("authentication is disabled"))
		return
	}

	tokens, _, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken, h.sessionMeta(c))
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Success(c, tokens)
}

func (h *Handler) logout(c *gin.Context) {
	identity, _ := auth.FromGin(c)
	if h.auth == nil {
		api.NoContent(c)
		return
	}
	if err := h.auth.Logout(c.Request.Context(), identity.SessionID); err != nil {
		api.Fail(c, err)
		return
	}
	h.record(c, "auth.logout", "success", identity.UserID, nil)
	api.NoContent(c)
}

func (h *Handler) me(c *gin.Context) {
	identity, _ := auth.FromGin(c)
	api.Success(c, identity)
}

func (h *Handler) systemInfo(c *gin.Context) {
	app := config.App()
	api.Success(c, gin.H{
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
		RequestID: api.RequestID(c),
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	}
	if err := h.auditor.Record(c.Request.Context(), event); err != nil {
		logger.Get().Warn("audit record failed")
	}
}
