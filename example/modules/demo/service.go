package demo

import (
	"context"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

// Service 承载示例业务逻辑，不依赖 Gin 与 GORM。
type Service struct {
	users  UserRepository
	hasher auth.PasswordHasher
}

// NewService 创建示例业务服务。
func NewService(users UserRepository, hasher auth.PasswordHasher) *Service {
	return &Service{users: users, hasher: hasher}
}

// CreateUser 创建用户并计算密码哈希。
func (s *Service) CreateUser(ctx context.Context, id, username, password string, roles, permissions []string) error {
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	return s.users.Create(ctx, &User{
		ID:           id,
		Username:     username,
		PasswordHash: hash,
		Roles:        roles,
		Permissions:  permissions,
	})
}

// Authenticate 校验用户凭据并返回认证身份。
func (s *Service) Authenticate(ctx context.Context, username, password string) (*auth.Identity, error) {
	user, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, apperrors.Unauthorized("invalid username or password")
	}
	ok, err := s.hasher.Verify(password, user.PasswordHash)
	if err != nil || !ok {
		return nil, apperrors.Unauthorized("invalid username or password")
	}
	return &auth.Identity{
		UserID:      user.ID,
		Roles:       user.Roles,
		Permissions: user.Permissions,
		AuthType:    "user",
	}, nil
}
