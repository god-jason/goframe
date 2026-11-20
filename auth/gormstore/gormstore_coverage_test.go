package gormstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/god-jason/goframe/auth"
)

// TestRotateRefreshTokenUpdateError 覆盖 RotateRefreshToken 中 UPDATE 语句失败的分支。
func TestRotateRefreshTokenUpdateError(t *testing.T) {
	repository := newTestRepository(t)
	// 删除表使 UPDATE 失败，触发 line 166。
	if err := repository.db.Exec("DROP TABLE auth_refresh_tokens").Error; err != nil {
		t.Fatalf("drop table: %v", err)
	}
	next := &auth.RefreshToken{
		ID:        "next-1",
		SessionID: "s1",
		FamilyID:  "f1",
		TokenHash: "hash",
	}
	err := repository.RotateRefreshToken(context.Background(), "old-id", next, time.Now())
	if err == nil || !strings.Contains(err.Error(), "rotate token") {
		t.Fatalf("err = %v", err)
	}
}
