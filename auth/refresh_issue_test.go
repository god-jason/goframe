package auth

import (
	"context"
	"testing"
)

// blankUserSessionRepository 模拟持久化层脏数据：返回 UserID 为空的会话记录，
// 触发 Refresh 中 issueTokens 失败后的防御分支。
type blankUserSessionRepository struct {
	*MemorySessionRepository
}

func (r *blankUserSessionRepository) GetSession(ctx context.Context, id string) (*Session, error) {
	session, err := r.MemorySessionRepository.GetSession(ctx, id)
	if err != nil || session == nil {
		return session, err
	}
	session.UserID = ""
	return session, nil
}

func TestRefreshFailsWhenSessionUserMissing(t *testing.T) {
	ctx := context.Background()
	repo := &blankUserSessionRepository{MemorySessionRepository: NewMemorySessionRepository()}
	service := newTestService(t, repo)

	tokens, _, err := service.Login(ctx, &Identity{UserID: "u1"}, SessionMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, _, err := service.Refresh(ctx, tokens.RefreshToken, SessionMeta{}); err == nil {
		t.Fatal("expected issue tokens failure for session without user id")
	}
}
