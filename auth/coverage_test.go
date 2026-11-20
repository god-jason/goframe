package auth

// 本文件补充 auth 包的边界分支覆盖。
//
// 以下缺失块在当前 Go 版本与调用约束下逻辑不可达，作为防御分支保留：
//   - jwt.go:46、apikey.go:104、password.go:55、session.go:286、totp.go:34/146/193、
//     auth.go:285：Go 1.24 起 crypto/rand.Read 失败时直接 fatal panic、永不返回
//     error（见 go.dev/issue/66821），替换 crypto/rand.Reader 只会触发进程级
//     panic，err != nil 分支无法执行；
//   - auth.go:125-127、auth.go:186、boot.go:64：issueTokens 经包内直接调用可
//     覆盖其 IssueAccessToken 失败分支，但 Login/Refresh 调用前均已校验 UserID
//     非空，其错误传播分支与 newOpaqueToken 失败分支（即上一条不可达分支）经
//     公开入口均不可触发；Startup 的 Validate 已先行拒绝非法 TTL，NewService
//     失败分支同理不可达；
//   - jwt.go:96：SignedString 的 Claims 序列化与 HMAC 签名对 []byte 密钥（含空
//     密钥）均不会失败；
//   - jwt.go:136：ParseWithClaims 返回 err==nil 时 token.Valid 必为 true 且
//     Claims 必为传入的 *Claims；
//   - config.go:77：Validate 前一条规则已拒绝长度小于 32 的 secret，而
//     "changeme" 长度恒为 8，该分支永不达。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/god-jason/goframe/logger"
)

func TestIssueTokensAccessTokenFailure(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	service := &Service{
		cfg:  testAuthConfig(),
		jwt:  manager,
		repo: NewMemorySessionRepository(),
		log:  logger.NewNop(),
	}
	if _, _, err := service.issueTokens(context.Background(), time.Now().UTC(),
		&Session{ID: "session-1"}, &Identity{}, "", ""); err == nil {
		t.Fatal("expected issue tokens error for empty user id")
	}
}

func TestAuthenticateInvalidToken(t *testing.T) {
	service, err := NewService(testAuthConfig(), NewMemorySessionRepository(), logger.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("expected authenticate error for invalid token")
	}
}

func TestMemoryAPIKeyRepositoryConflictsAndMisses(t *testing.T) {
	repo := NewMemoryAPIKeyRepository()
	ctx := context.Background()
	if err := repo.Create(ctx, &APIKey{ID: "k-1", Prefix: "gf_shared"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Create(ctx, &APIKey{ID: "k-2", Prefix: "gf_shared"}); err == nil {
		t.Fatal("expected conflict for duplicated prefix")
	}
	if _, err := repo.GetByPrefix(ctx, "gf_missing"); err == nil {
		t.Fatal("expected not found for missing prefix")
	}
}

// nonHMACMethod 是 Alg 仍为 HS256 但并非 HMAC 实现的伪签名方法，
// 用于触发 ParseAccessToken 中 keyfunc 的签名方法类型校验分支。
type nonHMACMethod struct{}

func (nonHMACMethod) Alg() string { return "HS256" }

func (nonHMACMethod) Verify(string, []byte, any) error { return errors.New("verification unavailable") }

func (nonHMACMethod) Sign(string, any) ([]byte, error) { return nil, nil }

func TestParseAccessTokenUnexpectedMethod(t *testing.T) {
	manager, err := NewJWTManager(testAuthConfig())
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	signed, _, err := manager.IssueAccessToken(&Identity{UserID: "u1", SessionID: "s-1"})
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	jwt.RegisterSigningMethod(jwt.SigningMethodHS256.Alg(), func() jwt.SigningMethod { return nonHMACMethod{} })
	defer jwt.RegisterSigningMethod(jwt.SigningMethodHS256.Alg(), func() jwt.SigningMethod { return jwt.SigningMethodHS256 })

	if _, err := manager.ParseAccessToken(signed); err == nil {
		t.Fatal("expected parse error for unexpected signing method")
	}
}
