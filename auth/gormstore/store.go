// Package gormstore 提供基于 GORM 的认证会话仓库实现。
package gormstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/god-jason/goframe/auth"
	apperrors "github.com/god-jason/goframe/errors"
)

// Repository 是 GORM 实现的 auth.SessionRepository。
type Repository struct {
	db *gorm.DB
}

// New 创建仓库并自动迁移表结构。
func New(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("gormstore: database is nil")
	}
	if err := db.AutoMigrate(&sessionModel{}, &refreshTokenModel{}); err != nil {
		return nil, fmt.Errorf("gormstore: migrate: %w", err)
	}
	return &Repository{db: db}, nil
}

// CreateSession 创建会话。
func (r *Repository) CreateSession(ctx context.Context, session *auth.Session) error {
	if session == nil || session.ID == "" {
		return fmt.Errorf("gormstore: session id is required")
	}
	model := sessionToModel(session)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		if isDuplicate(err) {
			return apperrors.Conflict("session already exists")
		}
		return fmt.Errorf("gormstore: create session: %w", err)
	}
	return nil
}

// GetSession 读取会话。
func (r *Repository) GetSession(ctx context.Context, id string) (*auth.Session, error) {
	var model sessionModel
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&model).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("session not found")
		}
		return nil, fmt.Errorf("gormstore: get session: %w", err)
	}
	return modelToSession(&model), nil
}

// UpdateSession 更新会话。
func (r *Repository) UpdateSession(ctx context.Context, session *auth.Session) error {
	if session == nil {
		return fmt.Errorf("gormstore: session is nil")
	}
	model := sessionToModel(session)
	result := r.db.WithContext(ctx).Model(&sessionModel{}).Where("id = ?", session.ID).Updates(map[string]any{
		"user_id":        model.UserID,
		"tenant_id":      model.TenantID,
		"roles":          model.Roles,
		"permissions":    model.Permissions,
		"device_name":    model.DeviceName,
		"ip":             model.IP,
		"user_agent":     model.UserAgent,
		"last_active_at": model.LastActiveAt,
		"expires_at":     model.ExpiresAt,
		"revoked_at":     model.RevokedAt,
	})
	if result.Error != nil {
		return fmt.Errorf("gormstore: update session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperrors.NotFound("session not found")
	}
	return nil
}

// ListSessions 列出用户的会话。
func (r *Repository) ListSessions(ctx context.Context, userID string) ([]*auth.Session, error) {
	var models []sessionModel
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at desc").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("gormstore: list sessions: %w", err)
	}
	sessions := make([]*auth.Session, len(models))
	for i := range models {
		sessions[i] = modelToSession(&models[i])
	}
	return sessions, nil
}

// RevokeSession 吊销会话。
func (r *Repository) RevokeSession(ctx context.Context, id string, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&sessionModel{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", at)
	if result.Error != nil {
		return fmt.Errorf("gormstore: revoke session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return apperrors.NotFound("session not found")
	}
	return nil
}

// RevokeUserSessions 吊销用户的全部会话，可排除指定会话。
func (r *Repository) RevokeUserSessions(ctx context.Context, userID string, exceptID string, at time.Time) error {
	query := r.db.WithContext(ctx).Model(&sessionModel{}).
		Where("user_id = ? AND revoked_at IS NULL", userID)
	if exceptID != "" {
		query = query.Where("id <> ?", exceptID)
	}
	if err := query.Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("gormstore: revoke user sessions: %w", err)
	}
	return nil
}

// CreateRefreshToken 创建刷新令牌。
func (r *Repository) CreateRefreshToken(ctx context.Context, token *auth.RefreshToken) error {
	if token == nil || token.TokenHash == "" {
		return fmt.Errorf("gormstore: token hash is required")
	}
	model := tokenToModel(token)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		if isDuplicate(err) {
			return apperrors.Conflict("refresh token already exists")
		}
		return fmt.Errorf("gormstore: create refresh token: %w", err)
	}
	return nil
}

// GetRefreshToken 按令牌哈希读取。
func (r *Repository) GetRefreshToken(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	var model refreshTokenModel
	if err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&model).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("refresh token not found")
		}
		return nil, fmt.Errorf("gormstore: get refresh token: %w", err)
	}
	return modelToToken(&model), nil
}

// RotateRefreshToken 在事务内标记旧令牌已轮换并写入新令牌。
func (r *Repository) RotateRefreshToken(ctx context.Context, oldID string, next *auth.RefreshToken, at time.Time) error {
	if next == nil {
		return fmt.Errorf("gormstore: next token is nil")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&refreshTokenModel{}).
			Where("id = ? AND rotated_at IS NULL AND revoked_at IS NULL", oldID).
			Update("rotated_at", at)
		if result.Error != nil {
			return fmt.Errorf("gormstore: rotate token: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return apperrors.NotFound("refresh token not found")
		}
		model := tokenToModel(next)
		if err := tx.Create(&model).Error; err != nil {
			if isDuplicate(err) {
				return apperrors.Conflict("refresh token already exists")
			}
			return fmt.Errorf("gormstore: create rotated token: %w", err)
		}
		return nil
	})
}

// RevokeTokenFamily 吊销整个令牌家族。
func (r *Repository) RevokeTokenFamily(ctx context.Context, familyID string, at time.Time) error {
	if err := r.db.WithContext(ctx).Model(&refreshTokenModel{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("gormstore: revoke token family: %w", err)
	}
	return nil
}

// RevokeSessionTokens 吊销会话下的全部刷新令牌。
func (r *Repository) RevokeSessionTokens(ctx context.Context, sessionID string, at time.Time) error {
	if err := r.db.WithContext(ctx).Model(&refreshTokenModel{}).
		Where("session_id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("gormstore: revoke session tokens: %w", err)
	}
	return nil
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "Duplicate entry") ||
		strings.Contains(message, "duplicate key")
}

func encodeStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeStrings(value string) []string {
	if value == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil
	}
	return values
}

func sessionToModel(session *auth.Session) sessionModel {
	return sessionModel{
		ID:           session.ID,
		UserID:       session.UserID,
		TenantID:     session.TenantID,
		Roles:        encodeStrings(session.Roles),
		Permissions:  encodeStrings(session.Permissions),
		DeviceName:   session.DeviceName,
		IP:           session.IP,
		UserAgent:    session.UserAgent,
		CreatedAt:    session.CreatedAt,
		LastActiveAt: session.LastActiveAt,
		ExpiresAt:    session.ExpiresAt,
		RevokedAt:    session.RevokedAt,
	}
}

func modelToSession(model *sessionModel) *auth.Session {
	return &auth.Session{
		ID:           model.ID,
		UserID:       model.UserID,
		TenantID:     model.TenantID,
		Roles:        decodeStrings(model.Roles),
		Permissions:  decodeStrings(model.Permissions),
		DeviceName:   model.DeviceName,
		IP:           model.IP,
		UserAgent:    model.UserAgent,
		CreatedAt:    model.CreatedAt,
		LastActiveAt: model.LastActiveAt,
		ExpiresAt:    model.ExpiresAt,
		RevokedAt:    model.RevokedAt,
	}
}

func tokenToModel(token *auth.RefreshToken) refreshTokenModel {
	return refreshTokenModel{
		ID:            token.ID,
		SessionID:     token.SessionID,
		FamilyID:      token.FamilyID,
		TokenHash:     token.TokenHash,
		ParentTokenID: token.ParentTokenID,
		CreatedAt:     token.CreatedAt,
		ExpiresAt:     token.ExpiresAt,
		RotatedAt:     token.RotatedAt,
		RevokedAt:     token.RevokedAt,
	}
}

func modelToToken(model *refreshTokenModel) *auth.RefreshToken {
	return &auth.RefreshToken{
		ID:            model.ID,
		SessionID:     model.SessionID,
		FamilyID:      model.FamilyID,
		TokenHash:     model.TokenHash,
		ParentTokenID: model.ParentTokenID,
		CreatedAt:     model.CreatedAt,
		ExpiresAt:     model.ExpiresAt,
		RotatedAt:     model.RotatedAt,
		RevokedAt:     model.RevokedAt,
	}
}
