package demo

import (
	"context"
	"sync"

	apperrors "github.com/god-jason/goframe/errors"
)

// User 是示例业务用户。
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Roles        []string
	Permissions  []string
}

func (u *User) clone() *User {
	if u == nil {
		return nil
	}
	clone := *u
	clone.Roles = append([]string(nil), u.Roles...)
	clone.Permissions = append([]string(nil), u.Permissions...)
	return &clone
}

// UserRepository 是用户仓储接口。Service 只依赖该接口，不依赖 GORM。
type UserRepository interface {
	GetByUsername(ctx context.Context, username string) (*User, error)
	Create(ctx context.Context, user *User) error
}

// MemoryUserRepository 是示例用内存仓储。生产环境请替换为 GORM 实现。
type MemoryUserRepository struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewMemoryUserRepository 创建内存用户仓储。
func NewMemoryUserRepository() *MemoryUserRepository {
	return &MemoryUserRepository{users: make(map[string]*User)}
}

// GetByUsername 按用户名查询用户。
func (r *MemoryUserRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, ok := r.users[username]
	if !ok {
		return nil, apperrors.NotFound("user not found")
	}
	return user.clone(), nil
}

// Create 创建用户。
func (r *MemoryUserRepository) Create(ctx context.Context, user *User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[user.Username]; ok {
		return apperrors.Conflict("user already exists")
	}
	r.users[user.Username] = user.clone()
	return nil
}
