package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDecryptCBCBadKey 覆盖 decryptCBC 中 aes.NewCipher 因非法密钥长度失败的分支。
func TestDecryptCBCBadKey(t *testing.T) {
	if _, err := decryptCBC([]byte{1, 2, 3}, make([]byte, 16), []byte("0123456789abcdef"), 16); err == nil ||
		!strings.Contains(err.Error(), "create cipher") {
		t.Fatalf("err = %v, want create cipher error", err)
	}
}

// TestDoJSONOutTypeMismatch 覆盖 doJSON 第二次反序列化（写入 out）失败的分支：
// errcode 为 0 但 openid 是数字，无法解析进 OAuthToken。
func TestDoJSONOutTypeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "openid": 123})
	}))
	defer server.Close()

	client := newTestClient(t, KindOfficial, server.URL)
	if _, err := client.ExchangeCode(context.Background(), "code-1"); err == nil ||
		!strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode response error", err)
	}
}

// TestCode2SessionHTTPError 覆盖 Code2Session 中 getJSON 失败返回的分支（HTTP 500）。
func TestCode2SessionHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, KindMini, server.URL)
	if _, err := client.Code2Session(context.Background(), "code-1"); err == nil {
		t.Fatal("expected error")
	}
}

// TestExchangeCodeAPIError 覆盖 ExchangeCode 中 getJSON 返回微信业务错误的分支。
func TestExchangeCodeAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 40029, "errmsg": "invalid code"})
	}))
	defer server.Close()

	client := newTestClient(t, KindOfficial, server.URL)
	if _, err := client.ExchangeCode(context.Background(), "bad"); err == nil {
		t.Fatal("expected error")
	}
}

// TestRequireKindMismatch 覆盖跨类型调用时的 requireKind 拦截分支。
func TestRequireKindMismatch(t *testing.T) {
	mini := newTestClient(t, KindMini, "")
	official := newTestClient(t, KindOfficial, "")

	if _, err := mini.RefreshOAuthToken(context.Background(), "rt"); err == nil {
		t.Fatal("mini should reject RefreshOAuthToken")
	}
	if _, err := mini.OAuthUserInfo(context.Background(), "at", "openid"); err == nil {
		t.Fatal("mini should reject OAuthUserInfo")
	}
	if _, err := official.PhoneNumber(context.Background(), "code"); err == nil {
		t.Fatal("official should reject PhoneNumber")
	}
}

// TestDecryptUserInfoBadSessionKey 覆盖 DecryptUserInfo 中 DecryptData 失败的分支。
func TestDecryptUserInfoBadSessionKey(t *testing.T) {
	client := newTestClient(t, KindMini, "")
	if _, err := client.DecryptUserInfo("!!!not-base64!!!", "abc", "def"); err == nil {
		t.Fatal("expected error")
	}
}

// TestJSAPITicketGetTicketError 覆盖 JSAPITicket 流程中 getticket 业务错误的分支：
// token 获取成功，但 getticket 返回非 token 失效的 errcode。
func TestJSAPITicketGetTicketError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 7200})
		case "/cgi-bin/ticket/getticket":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 45009, "errmsg": "api freq out"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(t, KindOfficial, server.URL)
	if _, err := client.JSAPITicket(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// TestWithAccessTokenFetchError 覆盖 withAccessToken 中 AccessToken 获取失败直接返回的分支。
func TestWithAccessTokenFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 40013, "errmsg": "invalid appid"})
	}))
	defer server.Close()

	client := newTestClient(t, KindOfficial, server.URL)
	err := client.withAccessToken(context.Background(), func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestTokenStoreSaveTinyTTL 覆盖 save 中 lifetime 收敛为 0 时回退 30s 的分支。
func TestTokenStoreSaveTinyTTL(t *testing.T) {
	store := newTokenStore(nil, "tiny-ttl")
	store.save(context.Background(), "v", time.Nanosecond)
	if got := store.load(context.Background()); got != "v" {
		t.Fatalf("load = %q, want v", got)
	}
}

// gateCache 是带门控的缓存桩：第 3 次 Get 阻塞，用于确定性地复现
// singleflight 闭包内二次检查命中缓存的竞态窗口。
type gateCache struct {
	calls     atomic.Int32
	bAtCache  chan struct{}
	fetchDone chan struct{}
}

func (c *gateCache) Get(ctx context.Context, key string, value any) error {
	if c.calls.Add(1) == 3 {
		close(c.bAtCache)
		<-c.fetchDone
	}
	return errors.New("cache: miss")
}

func (c *gateCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return nil
}

func (c *gateCache) Delete(ctx context.Context, key string) error { return nil }

func (c *gateCache) Exists(ctx context.Context, key string) (bool, error) { return false, nil }

func (c *gateCache) Close() error { return nil }

// TestTokenStoreGetInnerHit 覆盖 get 在 singleflight 闭包内二次 load 命中本地缓存的分支：
// A 取号并保存期间，B 已通过外层 load（缓存桩门控阻塞）确认无值，
// B 进入闭包时 A 已保存，B 的闭包直接命中返回值而不触发 fetch。
func TestTokenStoreGetInnerHit(t *testing.T) {
	gate := &gateCache{bAtCache: make(chan struct{}), fetchDone: make(chan struct{})}
	store := newTokenStore(gate, "gate-key")
	ctx := context.Background()
	fetchStarted := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		value, err := store.get(ctx, func(context.Context) (string, time.Duration, error) {
			close(fetchStarted)
			<-gate.bAtCache // 等 B 卡在缓存 Get 后再返回
			return "tok-a", time.Hour, nil
		})
		if err != nil || value != "tok-a" {
			t.Errorf("A get = (%q, %v)", value, err)
		}
		close(gate.fetchDone) // A 的 save 已完成，放行 B
	}()

	<-fetchStarted // 此时 A 已完成外层与内层前两次 Get
	go func() {
		defer wg.Done()
		value, err := store.get(ctx, func(context.Context) (string, time.Duration, error) {
			t.Error("B 不应触发 fetch")
			return "", 0, nil
		})
		if err != nil || value != "tok-a" {
			t.Errorf("B get = (%q, %v)", value, err)
		}
	}()
	wg.Wait()
}

// nilHTTPClientOption 把 httpClient 置空，触发 New 内回退默认客户端的分支。
func nilHTTPClientOption(c *Client) { c.httpClient = nil }

// TestNewNilHTTPClientFallback 覆盖 New 中 client.httpClient 为 nil 时创建默认客户端的分支。
func TestNewNilHTTPClientFallback(t *testing.T) {
	client, err := New(testApp(KindMini), nilHTTPClientOption)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.httpClient == nil {
		t.Fatal("httpClient should be defaulted")
	}
}

// TestStartupZeroTimeout 覆盖 Startup 中配置 timeout<=0 时回退 10s 的分支。
func TestStartupZeroTimeout(t *testing.T) {
	writeConfig(t, `
weixin:
  enabled: true
  timeout: 0
  apps:
    - name: zero
      type: mini
      app_id: id-zero
      app_secret: sec-zero
`)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background()) })
	if client := Get("zero"); client == nil || client.timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", client.timeout)
	}
}

// 说明：crypto.go 中 randomNonce 的 rand.Read 错误分支不可达——
// Go 1.24+ 的 crypto/rand.Read 出错时直接 fatal 终止进程，永不返回 error。
