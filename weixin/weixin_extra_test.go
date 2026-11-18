package weixin

import (
	"context"
	"crypto/aes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/god-jason/goframe/cache"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

// newJSONServer 启动一个按路径分发的假微信服务。
func newJSONServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return server
}

func writeJSON(w http.ResponseWriter, value string) {
	_, _ = io.WriteString(w, value)
}

func TestClientAccessors(t *testing.T) {
	client := newTestClient(t, KindOfficial, "")
	if client.Name() != "test-official" || client.Kind() != KindOfficial || client.AppID() != "wx-test-app" {
		t.Fatalf("accessors = %q, %q, %q", client.Name(), client.Kind(), client.AppID())
	}

	var nilClient *Client
	if err := nilClient.requireKind(KindMini); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigHelpers(t *testing.T) {
	if DefaultConfig().Timeout != 10*time.Second {
		t.Fatalf("default config = %+v", DefaultConfig())
	}
	cases := map[string]Kind{
		"official":           KindOfficial,
		" official_account ": KindOfficial,
		"MP":                 KindOfficial,
		"gzh":                KindOfficial,
		"mini":               KindMini,
		"mini_program":       KindMini,
		"MiniProgram":        KindMini,
		"xcx":                KindMini,
		"unknown":            Kind(""),
		"":                   Kind(""),
	}
	for input, want := range cases {
		if got := NormalizeKind(input); got != want {
			t.Fatalf("NormalizeKind(%q) = %q, want %q", input, got, want)
		}
	}
	if GetConfig() == nil {
		t.Fatal("GetConfig should not be nil")
	}

	if err := (AppConfig{Type: "mini", AppID: "a", AppSecret: "b"}).Validate(); err == nil {
		t.Fatal("expected missing name")
	}
	if err := (AppConfig{Name: "x", Type: "mini", AppID: "a"}).Validate(); err == nil {
		t.Fatal("expected missing secret")
	}
	if err := (AppConfig{Name: "x", Type: "mini", AppID: "a", AppSecret: "b", EncodingAESKey: "short"}).Validate(); err == nil {
		t.Fatal("expected invalid aes key length")
	}
	if err := (AppConfig{Name: "x", Type: "mini", AppID: "a", AppSecret: "b", EncodingAESKey: strings.Repeat("a", 43)}).Validate(); err != nil {
		t.Fatalf("valid aes key: %v", err)
	}
}

func TestOptionsAndNewValidation(t *testing.T) {
	mem := cache.NewMemory()
	defer mem.Close()
	httpClient := &http.Client{}

	client, err := New(testApp(KindMini),
		nil,
		WithCache(mem),
		WithLogger(logger.NewNop()),
		WithHTTPClient(httpClient),
		WithBaseURL(""),
		WithTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.cache != mem || client.httpClient != httpClient || client.timeout != 2*time.Second {
		t.Fatalf("client = %+v", client)
	}

	if _, err := New(testApp(KindMini), WithHTTPClient(nil)); err != nil {
		t.Fatalf("nil http client should be ignored: %v", err)
	}

	app := testApp(KindOfficial)
	app.EncodingAESKey = strings.Repeat("!", 43)
	if _, err := New(app); err == nil {
		t.Fatal("expected invalid encoding_aes_key")
	}
}

func TestAPIError(t *testing.T) {
	err := &APIError{Code: 40001, Message: "invalid token", Status: 200, Path: "/cgi-bin/token"}
	if !err.TokenInvalid() {
		t.Fatal("40001 should be token invalid")
	}
	for _, code := range []int{40014, 42001} {
		if !(&APIError{Code: code}).TokenInvalid() {
			t.Fatalf("code %d should be token invalid", code)
		}
	}
	if (&APIError{Code: 0}).TokenInvalid() {
		t.Fatal("code 0 should not be token invalid")
	}
	message := err.Error()
	for _, part := range []string{"/cgi-bin/token", "status=200", "errcode=40001", "invalid token"} {
		if !strings.Contains(message, part) {
			t.Fatalf("error %q missing %q", message, part)
		}
	}
}

func TestAccessTokenErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"empty json", http.StatusOK, `{}`},
		{"empty body", http.StatusOK, ``},
		{"errcode", http.StatusOK, `{"errcode":40001,"errmsg":"bad token"}`},
		{"bad json", http.StatusOK, `{"access_token":`},
		{"http error", http.StatusInternalServerError, `server down`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				writeJSON(w, tc.body)
			})
			client := newTestClient(t, KindMini, server.URL)
			if _, err := client.AccessToken(context.Background()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAccessTokenSharedCache(t *testing.T) {
	var calls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, `{"access_token":"SHARED","expires_in":7200}`)
	})

	mem := cache.NewMemory()
	defer mem.Close()
	first, err := New(testApp(KindMini), WithBaseURL(server.URL), WithCache(mem))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := New(testApp(KindMini), WithBaseURL(server.URL), WithCache(mem))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, client := range []*Client{first, second} {
		token, err := client.AccessToken(context.Background())
		if err != nil {
			t.Fatalf("AccessToken: %v", err)
		}
		if token != "SHARED" {
			t.Fatalf("token = %q", token)
		}
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestTokenStoreCacheExpiryAndSave(t *testing.T) {
	ctx := context.Background()
	mem := cache.NewMemory()
	defer mem.Close()

	store := newTokenStore(mem, "key")
	if err := mem.Set(ctx, "key", cachedToken{Value: "old", ExpiresAt: time.Now().Add(-time.Second)}, time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	fetched := false
	value, err := store.get(ctx, func(context.Context) (string, time.Duration, error) {
		fetched = true
		return "fresh", time.Hour, nil
	})
	if err != nil || value != "fresh" || !fetched {
		t.Fatalf("value = %q, err = %v, fetched = %v", value, err, fetched)
	}

	// 缓存中 value 为空时同样触发刷新。
	store = newTokenStore(mem, "empty")
	if err := mem.Set(ctx, "empty", cachedToken{Value: "", ExpiresAt: time.Now().Add(time.Hour)}, time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	value, err = store.get(ctx, func(context.Context) (string, time.Duration, error) {
		return "next", time.Hour, nil
	})
	if err != nil || value != "next" {
		t.Fatalf("value = %q, err = %v", value, err)
	}

	// 缓存未命中时刷新。
	store = newTokenStore(cache.NewMemory(), "missing")
	value, err = store.get(ctx, func(context.Context) (string, time.Duration, error) {
		return "miss", time.Hour, nil
	})
	if err != nil || value != "miss" {
		t.Fatalf("value = %q, err = %v", value, err)
	}

	// fetch 出错时不缓存。
	store.invalidate(ctx)
	if _, err := store.get(ctx, func(context.Context) (string, time.Duration, error) {
		return "", 0, errors.New("fetch failed")
	}); err == nil {
		t.Fatal("expected fetch error")
	}

	// save 的 TTL 分支。
	saveStore := newTokenStore(nil, "save")
	saveStore.save(ctx, "a", 0)
	if !saveStore.expiresAt.After(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("zero ttl expiresAt = %v", saveStore.expiresAt)
	}
	saveStore.save(ctx, "b", 20*time.Second)
	if !saveStore.expiresAt.After(time.Now().Add(5 * time.Second)) {
		t.Fatalf("short ttl expiresAt = %v", saveStore.expiresAt)
	}
	saveStore.save(ctx, "c", time.Hour)
	saveStore.invalidate(ctx) // 无共享缓存时也应正常工作。

	// invalidate 同时清理共享缓存。
	cacheStore := newTokenStore(mem, "invalidate")
	cacheStore.save(ctx, "token", time.Hour)
	cacheStore.invalidate(ctx)
	exists, err := mem.Exists(ctx, "invalidate")
	if err != nil || exists {
		t.Fatalf("exists = %v, err = %v", exists, err)
	}
}

func TestAccessTokenConcurrentSingleflight(t *testing.T) {
	var calls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		writeJSON(w, `{"access_token":"CONCURRENT","expires_in":7200}`)
	})
	client := newTestClient(t, KindMini, server.URL)

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := client.AccessToken(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if token != "CONCURRENT" {
				errs <- fmt.Errorf("token = %q", token)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent token: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestWithAccessTokenRetriesOnTokenInvalid(t *testing.T) {
	var tokenCalls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			call := atomic.AddInt32(&tokenCalls, 1)
			writeJSON(w, fmt.Sprintf(`{"access_token":"TOKEN-%d","expires_in":7200}`, call))
		case "/wxa/business/getuserphonenumber":
			if r.URL.Query().Get("access_token") == "TOKEN-1" {
				writeJSON(w, `{"errcode":40001,"errmsg":"invalid token"}`)
				return
			}
			writeJSON(w, `{"errcode":0,"phone_info":{"purePhoneNumber":"13800138000"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	client := newTestClient(t, KindMini, server.URL)

	phone, err := client.PhoneNumber(context.Background(), "code")
	if err != nil {
		t.Fatalf("PhoneNumber: %v", err)
	}
	if phone.PurePhoneNumber != "13800138000" {
		t.Fatalf("phone = %+v", phone)
	}
	if atomic.LoadInt32(&tokenCalls) != 2 {
		t.Fatalf("token calls = %d, want 2", tokenCalls)
	}
}

func TestWithAccessTokenRetryFailsWhenRefreshFails(t *testing.T) {
	var tokenCalls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			if atomic.AddInt32(&tokenCalls, 1) == 1 {
				writeJSON(w, `{"access_token":"TOKEN","expires_in":7200}`)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, `boom`)
		case "/wxa/business/getuserphonenumber":
			writeJSON(w, `{"errcode":40001,"errmsg":"invalid token"}`)
		}
	})
	client := newTestClient(t, KindMini, server.URL)
	if _, err := client.PhoneNumber(context.Background(), "code"); err == nil {
		t.Fatal("expected refresh failure")
	}
}

func TestInvalidateAccessToken(t *testing.T) {
	var calls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(w, fmt.Sprintf(`{"access_token":"T%d","expires_in":7200}`, calls))
	})
	client := newTestClient(t, KindMini, server.URL)
	if _, err := client.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	client.invalidateAccessToken(context.Background())
	if _, err := client.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestOAuthFlows(t *testing.T) {
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sns/oauth2/access_token":
			if r.URL.Query().Get("code") != "code-1" {
				t.Errorf("code = %q", r.URL.Query().Get("code"))
			}
			writeJSON(w, `{"access_token":"OAUTH","expires_in":7200,"refresh_token":"REFRESH","openid":"OPENID","scope":"snsapi_base","unionid":"UNION"}`)
		case "/sns/oauth2/refresh_token":
			if r.URL.Query().Get("refresh_token") != "REFRESH" {
				t.Errorf("refresh_token = %q", r.URL.Query().Get("refresh_token"))
			}
			writeJSON(w, `{"access_token":"OAUTH2","expires_in":7200,"refresh_token":"REFRESH2","openid":"OPENID"}`)
		case "/sns/userinfo":
			if r.URL.Query().Get("openid") != "OPENID" {
				t.Errorf("openid = %q", r.URL.Query().Get("openid"))
			}
			writeJSON(w, `{"openid":"OPENID","nickname":"nick","sex":1,"privilege":["a"],"unionid":"UNION"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	app := testApp(KindOfficial)
	app.OAuthRedirect = "https://example.com/default"
	client, err := New(app, WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	token, err := client.ExchangeCode(ctx, "code-1")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.OpenID != "OPENID" || token.RefreshToken != "REFRESH" || token.UnionID != "UNION" {
		t.Fatalf("token = %+v", token)
	}

	refreshed, err := client.RefreshOAuthToken(ctx, "REFRESH")
	if err != nil {
		t.Fatalf("RefreshOAuthToken: %v", err)
	}
	if refreshed.AccessToken != "OAUTH2" || refreshed.RefreshToken != "REFRESH2" {
		t.Fatalf("refreshed = %+v", refreshed)
	}

	info, err := client.OAuthUserInfo(ctx, "OAUTH", "OPENID")
	if err != nil {
		t.Fatalf("OAuthUserInfo: %v", err)
	}
	if info.Nickname != "nick" || info.UnionID != "UNION" || len(info.Privilege) != 1 {
		t.Fatalf("info = %+v", info)
	}

	url := client.AuthorizeURL("", "snsapi_userinfo", "state")
	for _, part := range []string{"scope=snsapi_userinfo", "redirect_uri=https%3A%2F%2Fexample.com%2Fdefault", "state=state"} {
		if !strings.Contains(url, part) {
			t.Fatalf("url %q missing %q", url, part)
		}
	}
}

func TestOAuthErrors(t *testing.T) {
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sns/oauth2/access_token":
			writeJSON(w, `{"access_token":"TOKEN"}`)
		default:
			writeJSON(w, `{"errcode":40029,"errmsg":"invalid code"}`)
		}
	})
	client := newTestClient(t, KindOfficial, server.URL)
	if _, err := client.ExchangeCode(context.Background(), "code"); err == nil || !strings.Contains(err.Error(), "openid") {
		t.Fatalf("err = %v", err)
	}
	if _, err := client.RefreshOAuthToken(context.Background(), "bad"); err == nil {
		t.Fatal("expected refresh error")
	}
	if _, err := client.OAuthUserInfo(context.Background(), "bad", "openid"); err == nil {
		t.Fatal("expected userinfo error")
	}
}

func TestJSAPITicketAndConfig(t *testing.T) {
	var tokenCalls, ticketCalls int32
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			atomic.AddInt32(&tokenCalls, 1)
			writeJSON(w, `{"access_token":"ACCESS","expires_in":7200}`)
		case "/cgi-bin/ticket/getticket":
			atomic.AddInt32(&ticketCalls, 1)
			writeJSON(w, `{"errcode":0,"ticket":"TICKET","expires_in":7200}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	client := newTestClient(t, KindOfficial, server.URL)
	ctx := context.Background()

	ticket, err := client.JSAPITicket(ctx)
	if err != nil {
		t.Fatalf("JSAPITicket: %v", err)
	}
	if ticket != "TICKET" {
		t.Fatalf("ticket = %q", ticket)
	}
	if _, err := client.JSAPITicket(ctx); err != nil {
		t.Fatalf("JSAPITicket cached: %v", err)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 || atomic.LoadInt32(&ticketCalls) != 1 {
		t.Fatalf("token calls = %d, ticket calls = %d", tokenCalls, ticketCalls)
	}

	config, err := client.JSSDKConfig(ctx, "https://example.com/page")
	if err != nil {
		t.Fatalf("JSSDKConfig: %v", err)
	}
	if config.AppID != client.AppID() || config.NonceStr == "" || config.Signature == "" {
		t.Fatalf("config = %+v", config)
	}
	raw := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", "TICKET", config.NonceStr, config.Timestamp, "https://example.com/page")
	sum := sha1.Sum([]byte(raw))
	if config.Signature != hex.EncodeToString(sum[:]) {
		t.Fatalf("signature = %q", config.Signature)
	}
}

func TestJSAPITicketErrors(t *testing.T) {
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(w, `{"access_token":"ACCESS","expires_in":7200}`)
		default:
			writeJSON(w, `{"errcode":0,"ticket":""}`)
		}
	})
	client := newTestClient(t, KindOfficial, server.URL)
	if _, err := client.JSAPITicket(context.Background()); err == nil {
		t.Fatal("expected empty ticket error")
	}
	if _, err := client.JSSDKConfig(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected JSSDKConfig error")
	}

	mini := newTestClient(t, KindMini, "")
	if _, err := mini.JSAPITicket(context.Background()); err == nil {
		t.Fatal("mini must not fetch jsapi ticket")
	}
}

func TestCode2SessionErrors(t *testing.T) {
	cases := []string{
		`{"session_key":"S"}`,
		`{"openid":"O"}`,
	}
	for _, body := range cases {
		server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, body)
		})
		client := newTestClient(t, KindMini, server.URL)
		if _, err := client.Code2Session(context.Background(), "code"); err == nil {
			t.Fatalf("expected error for %s", body)
		}
	}
}

func TestPhoneNumberEmpty(t *testing.T) {
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(w, `{"access_token":"ACCESS","expires_in":7200}`)
		default:
			writeJSON(w, `{"errcode":0,"phone_info":{"purePhoneNumber":""}}`)
		}
	})
	client := newTestClient(t, KindMini, server.URL)
	if _, err := client.PhoneNumber(context.Background(), "code"); err == nil {
		t.Fatal("expected empty phone error")
	}

	server2 := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(w, `{"access_token":"ACCESS","expires_in":7200}`)
		default:
			writeJSON(w, `{"errcode":0}`)
		}
	})
	client2 := newTestClient(t, KindMini, server2.URL)
	if _, err := client2.PhoneNumber(context.Background(), "code"); err == nil {
		t.Fatal("expected missing phone_info error")
	}
}

func TestDecryptDataErrors(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	validIV := base64.StdEncoding.EncodeToString([]byte("abcdef0123456789"))
	cases := []struct {
		name    string
		key     string
		payload string
		iv      string
	}{
		{"bad key base64", "!!!", "AAAA", validIV},
		{"bad key length", base64.StdEncoding.EncodeToString([]byte("short")), "AAAA", validIV},
		{"bad iv base64", validKey, "AAAA", "!!!"},
		{"bad iv length", validKey, "AAAA", base64.StdEncoding.EncodeToString([]byte("short"))},
		{"bad data base64", validKey, "!!!", validIV},
		{"bad ciphertext length", validKey, base64.StdEncoding.EncodeToString([]byte("short")), validIV},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecryptData(tc.key, tc.payload, tc.iv); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDecryptCBCAndPadding(t *testing.T) {
	key := []byte("0123456789abcdef")
	if _, err := decryptCBC(key, key[:16], nil, aes.BlockSize); err == nil {
		t.Fatal("expected empty ciphertext error")
	}
	if _, err := decryptCBC(key, key[:16], []byte("short"), aes.BlockSize); err == nil {
		t.Fatal("expected invalid ciphertext length")
	}

	if _, err := pkcs7Unpad(nil, aes.BlockSize); err == nil {
		t.Fatal("expected empty padding error")
	}
	if _, err := pkcs7Unpad([]byte("short"), aes.BlockSize); err == nil {
		t.Fatal("expected invalid padding length")
	}
	if _, err := pkcs7Unpad(bytesRepeat(0, aes.BlockSize), aes.BlockSize); err == nil {
		t.Fatal("expected padding size 0 error")
	}
	if _, err := pkcs7Unpad(bytesRepeat(17, aes.BlockSize), aes.BlockSize); err == nil {
		t.Fatal("expected padding too large error")
	}
	invalid := bytesRepeat(1, aes.BlockSize)
	invalid[aes.BlockSize-1] = 3
	if _, err := pkcs7Unpad(invalid, aes.BlockSize); err == nil {
		t.Fatal("expected padding bytes error")
	}
	if got, err := pkcs7Unpad(bytesRepeat(4, aes.BlockSize), aes.BlockSize); err != nil || len(got) != aes.BlockSize-4 {
		t.Fatalf("valid unpad = %d, %v", len(got), err)
	}
}

func TestDecryptMessageErrors(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	aesKey := strings.TrimRight(base64.StdEncoding.EncodeToString(key), "=")
	app := testApp(KindOfficial)
	app.EncodingAESKey = aesKey
	client, err := New(app)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	noKey := newTestClient(t, KindOfficial, "")
	if _, err := noKey.DecryptMessage("abc"); err == nil {
		t.Fatal("expected missing aes key error")
	}
	if _, err := client.DecryptMessage("!!!"); err == nil {
		t.Fatal("expected invalid base64 error")
	}
	if _, err := client.DecryptMessage(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected invalid ciphertext error")
	}

	// 明文过短（去填充后小于 20 字节）。
	short := encryptCBC(key, key[:16], pkcs7Pad(bytesRepeat(0, 16), 32))
	if _, err := client.DecryptMessage(base64.StdEncoding.EncodeToString(short)); err == nil {
		t.Fatal("expected short plaintext error")
	}

	// appid 不匹配。
	mismatch := buildEncryptedMessage(t, key, "other-app", []byte("<xml/>"), 6)
	if _, err := client.DecryptMessage(mismatch); err == nil || !strings.Contains(err.Error(), "appid") {
		t.Fatalf("err = %v", err)
	}

	// 消息长度非法。
	invalidLenData := buildEncryptedMessage(t, key, app.AppID, []byte("hi"), 9999)
	if _, err := client.DecryptMessage(invalidLenData); err == nil {
		t.Fatal("expected invalid message length")
	}

	valid := buildEncryptedMessage(t, key, app.AppID, []byte("<xml>ok</xml>"), len([]byte("<xml>ok</xml>")))
	data, err := client.DecryptMessage(valid)
	if err != nil || string(data) != "<xml>ok</xml>" {
		t.Fatalf("data = %q, err = %v", data, err)
	}
}

func buildEncryptedMessage(t *testing.T, key []byte, appID string, message []byte, messageLen int) string {
	t.Helper()
	plaintext := make([]byte, 16)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(messageLen))
	plaintext = append(plaintext, length[:]...)
	plaintext = append(plaintext, message...)
	plaintext = append(plaintext, []byte(appID)...)
	encrypted := encryptCBC(key, key[:16], pkcs7Pad(plaintext, 32))
	return base64.StdEncoding.EncodeToString(encrypted)
}

func TestDecryptPhoneAndUserInfoErrors(t *testing.T) {
	client := newTestClient(t, KindMini, "")
	if _, err := client.DecryptPhone("!!!", "AAAA", "AAAA"); err == nil {
		t.Fatal("expected decrypt error")
	}

	sessionKey := []byte("0123456789abcdef")
	iv := []byte("abcdef0123456789")
	emptyPhone := []byte(`{"purePhoneNumber":""}`)
	encrypted := encryptCBC(sessionKey, iv, pkcs7Pad(emptyPhone, aes.BlockSize))
	if _, err := client.DecryptPhone(
		base64.StdEncoding.EncodeToString(sessionKey),
		base64.StdEncoding.EncodeToString(encrypted),
		base64.StdEncoding.EncodeToString(iv),
	); err == nil {
		t.Fatal("expected empty phone error")
	}

	badJSON := []byte("not-json")
	encrypted = encryptCBC(sessionKey, iv, pkcs7Pad(badJSON, aes.BlockSize))
	if _, err := client.DecryptPhone(
		base64.StdEncoding.EncodeToString(sessionKey),
		base64.StdEncoding.EncodeToString(encrypted),
		base64.StdEncoding.EncodeToString(iv),
	); err == nil {
		t.Fatal("expected decode phone error")
	}
	if _, err := client.DecryptUserInfo(
		base64.StdEncoding.EncodeToString(sessionKey),
		base64.StdEncoding.EncodeToString(encrypted),
		base64.StdEncoding.EncodeToString(iv),
	); err == nil {
		t.Fatal("expected decode user error")
	}

	userJSON := []byte(`{"openId":"OPEN","nickName":"nick","gender":1,"city":"c","province":"p","country":"cn","avatarUrl":"http://a","unionId":"U"}`)
	encrypted = encryptCBC(sessionKey, iv, pkcs7Pad(userJSON, aes.BlockSize))
	user, err := client.DecryptUserInfo(
		base64.StdEncoding.EncodeToString(sessionKey),
		base64.StdEncoding.EncodeToString(encrypted),
		base64.StdEncoding.EncodeToString(iv),
	)
	if err != nil {
		t.Fatalf("DecryptUserInfo: %v", err)
	}
	if user.OpenID != "OPEN" || user.NickName != "nick" || user.UnionID != "U" {
		t.Fatalf("user = %+v", user)
	}
}

func TestRandomNonceAndVerifyHex(t *testing.T) {
	if got := randomNonce(0); len(got) != 16 {
		t.Fatalf("randomNonce(0) = %q", got)
	}
	if got := randomNonce(-1); len(got) != 16 {
		t.Fatalf("randomNonce(-1) = %q", got)
	}
	if got := randomNonce(5); len(got) != 5 {
		t.Fatalf("randomNonce(5) = %q", got)
	}

	if !verifyHex("ABCDEF", " abcdef ") {
		t.Fatal("verifyHex should ignore case and whitespace")
	}
	if verifyHex("abcd", "abcde") {
		t.Fatal("verifyHex should reject length mismatch")
	}
	if verifyHex("abcd", "abce") {
		t.Fatal("verifyHex should reject different values")
	}
}

func TestHTTPJSONErrors(t *testing.T) {
	var nilClient *Client
	if err := nilClient.getJSON(context.Background(), "/x", nil); err == nil {
		t.Fatal("expected nil client error")
	}

	// 构造请求失败。
	broken := newTestClient(t, KindMini, "://bad")
	if _, err := broken.AccessToken(context.Background()); err == nil {
		t.Fatal("expected request creation error")
	}

	// 请求体编码失败。
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {})
	client := newTestClient(t, KindMini, server.URL)
	if err := client.postJSON(context.Background(), "/x", make(chan int), nil); err == nil {
		t.Fatal("expected encode error")
	}

	// 响应体读取失败。
	client = newTestClient(t, KindMini, "http://example.com")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{err: errors.New("read failed")}, Header: http.Header{}}, nil
	})}
	if _, err := client.AccessToken(context.Background()); err == nil {
		t.Fatal("expected read error")
	}

	// 传输层错误。
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	if _, err := client.AccessToken(context.Background()); err == nil {
		t.Fatal("expected transport error")
	}

	// nil context 与空响应体。
	server2 := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {})
	client = newTestClient(t, KindMini, server2.URL)
	var out struct{}
	if err := client.getJSON(nil, "/empty", &out); err != nil {
		t.Fatalf("getJSON nil ctx: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type errReadCloser struct{ err error }

func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r errReadCloser) Close() error             { return nil }

func TestDoJSONDecodeError(t *testing.T) {
	server := newJSONServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, "not-json")
	})
	client := newTestClient(t, KindMini, server.URL)
	var out struct{}
	if err := client.getJSON(context.Background(), "/x", &out); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestStartupConfig(t *testing.T) {
	writeConfig(t, `
weixin:
  enabled: true
  apps:
    - name: mini
      type: mini
      app_id: id-mini
      app_secret: sec-mini
      default: true
    - name: mp
      type: official
      app_id: id-mp
      app_secret: sec-mp
      token: tok
`)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background()) })

	if apps := Apps(); len(apps) != 2 || apps[0] != "mini" || apps[1] != "mp" {
		t.Fatalf("apps = %v", apps)
	}
	if client := Default(); client == nil || client.Name() != "mini" {
		t.Fatalf("default = %+v", client)
	}
	if client := Get("mp"); client == nil || client.Kind() != KindOfficial {
		t.Fatalf("Get(mp) = %+v", client)
	}
	if client := Get("missing"); client != nil {
		t.Fatalf("Get(missing) = %+v", client)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Default() != nil || len(Apps()) != 0 {
		t.Fatal("clients should be cleared after Shutdown")
	}
}

func TestStartupFallbackFirstApp(t *testing.T) {
	writeConfig(t, `
weixin:
  enabled: true
  apps:
    - name: first
      type: mini
      app_id: id-1
      app_secret: sec-1
    - name: second
      type: mini
      app_id: id-2
      app_secret: sec-2
`)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(context.Background()) })
	if client := Default(); client == nil || client.Name() != "first" {
		t.Fatalf("fallback = %+v", client)
	}
}

func TestStartupErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"apps empty", "weixin:\n  enabled: true\n  apps: []\n"},
		{"duplicate", `
weixin:
  enabled: true
  apps:
    - name: a
      type: mini
      app_id: id
      app_secret: sec
    - name: a
      type: mini
      app_id: id2
      app_secret: sec2
`},
		{"invalid app", `
weixin:
  enabled: true
  apps:
    - name: a
      type: nope
      app_id: id
      app_secret: sec
`},
		{"multiple defaults", `
weixin:
  enabled: true
  apps:
    - name: a
      type: mini
      app_id: id
      app_secret: sec
      default: true
    - name: b
      type: mini
      app_id: id2
      app_secret: sec2
      default: true
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.yaml)
			if err := Startup(context.Background()); err == nil {
				t.Fatal("expected Startup error")
			}
			_ = Shutdown(context.Background())
		})
	}
}

func TestStartupDisabled(t *testing.T) {
	writeConfig(t, "weixin:\n  enabled: false\n")
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get("") != nil {
		t.Fatal("disabled startup should not register clients")
	}
}

func writeConfig(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetFile(path)
	if err := config.Reload(); err != nil {
		t.Fatalf("config.Reload: %v", err)
	}
	t.Cleanup(func() {
		config.SetFile("")
		_ = config.Reload()
	})
}

var _ = json.Marshal
