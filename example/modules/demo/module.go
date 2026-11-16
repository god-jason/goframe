package demo

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/app"
	"github.com/god-jason/goframe/auth"
)

const (
	// DefaultUsername 是示例管理员账号。
	DefaultUsername = "admin"
	// DefaultPassword 是示例管理员密码，仅用于本地演示。
	DefaultPassword = "admin123456"
)

// Module 是示例业务模块。
type Module struct {
	handler *Handler
}

// New 创建示例模块。
func New() *Module { return &Module{} }

// Name 返回模块名称。
func (m *Module) Name() string { return "demo" }

// Init 初始化模块。
func (m *Module) Init(application *app.App) error {
	hasher, err := auth.NewPasswordHasher(application.Config.Security.Password)
	if err != nil {
		return err
	}

	service := NewService(NewMemoryUserRepository(), hasher)
	if err := service.CreateUser(context.Background(), "1", DefaultUsername, DefaultPassword,
		[]string{"admin"}, []string{"system:read", "user:read"}); err != nil {
		return err
	}

	m.handler = NewHandler(service, application.Auth, application)
	return nil
}

// RegisterRoutes 注册模块路由。
func (m *Module) RegisterRoutes(router gin.IRouter) {
	m.handler.RegisterRoutes(router)
}
