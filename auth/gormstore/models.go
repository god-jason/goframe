package gormstore

import "time"

// sessionModel 是会话表模型。
type sessionModel struct {
	ID           string    `gorm:"primaryKey;size:64"`
	UserID       string    `gorm:"index;size:64;not null"`
	TenantID     string    `gorm:"size:64"`
	Roles        string    `gorm:"type:text"`
	Permissions  string    `gorm:"type:text"`
	DeviceName   string    `gorm:"size:256"`
	IP           string    `gorm:"size:64"`
	UserAgent    string    `gorm:"size:512"`
	CreatedAt    time.Time `gorm:"index"`
	LastActiveAt time.Time
	ExpiresAt    time.Time  `gorm:"index"`
	RevokedAt    *time.Time `gorm:"index"`
}

func (sessionModel) TableName() string { return "auth_sessions" }

// refreshTokenModel 是刷新令牌表模型，只保存令牌哈希。
type refreshTokenModel struct {
	ID            string     `gorm:"primaryKey;size:64"`
	SessionID     string     `gorm:"index;size:64;not null"`
	FamilyID      string     `gorm:"index;size:64;not null"`
	TokenHash     string     `gorm:"uniqueIndex;size:128;not null"`
	ParentTokenID string     `gorm:"size:64"`
	CreatedAt     time.Time  `gorm:"index"`
	ExpiresAt     time.Time  `gorm:"index"`
	RotatedAt     *time.Time `gorm:"index"`
	RevokedAt     *time.Time `gorm:"index"`
}

func (refreshTokenModel) TableName() string { return "auth_refresh_tokens" }
