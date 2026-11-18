package demo

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/permission"
)

const (
	// DefaultUsername 是示例管理员账号。
	DefaultUsername = "admin"
	// DefaultPassword 是示例管理员密码，仅用于本地演示。
	DefaultPassword = "admin123456"
)

// 以下变量是测试注入点，生产环境保持默认实现。
var (
	currentAuthConfig = auth.GetConfig
	newPasswordHasher = func(cfg auth.PasswordConfig) (auth.PasswordHasher, error) {
		return auth.NewPasswordHasher(cfg)
	}
	currentAuthManager = auth.Get
	currentAuthorizer  = permission.Get
)

// Module 是示例业务模块。
type Module struct {
	handler *Handler
}

// New 创建示例模块。
func New() *Module { return &Module{} }

// Name 返回模块名称。
func (m *Module) Name() string { return "demo" }

// Init 初始化模块。业务模块在 boot.Startup 之后手动调用。
func (m *Module) Init() error {
	cfg := currentAuthConfig()
	hasher, err := newPasswordHasher(cfg.Password)
	if err != nil {
		return err
	}

	service := NewService(NewMemoryUserRepository(), hasher)
	if err := service.CreateUser(context.Background(), "1", DefaultUsername, DefaultPassword,
		[]string{"admin"}, []string{"system:read", "user:read"}); err != nil {
		return err
	}

	m.handler = NewHandler(service, currentAuthManager(), currentAuthorizer())
	return nil
}

// Mount 把本模块登记的接口统一挂载到 web 的 API 分组。
func (m *Module) Mount(router gin.IRoutes) error {
	return m.handler.Mount(router)
}
