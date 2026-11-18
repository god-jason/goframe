package weixin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testApp(kind Kind) AppConfig {
	return AppConfig{
		Name:      "test-" + string(kind),
		Type:      string(kind),
		AppID:     "wx-test-app",
		AppSecret: "wx-test-secret",
		Token:     "test-token",
	}
}

func newTestClient(t *testing.T, kind Kind, baseURL string) *Client {
	t.Helper()
	client, err := New(testApp(kind), WithBaseURL(baseURL), WithTimeout(3*time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestAuthorizeURL(t *testing.T) {
	client := newTestClient(t, KindOfficial, "")
	value := client.AuthorizeURL("https://example.com/cb", "", "state-1")
	if !strings.HasPrefix(value, "https://open.weixin.qq.com/connect/oauth2/authorize?") {
		t.Fatalf("url = %q", value)
	}
	if !strings.HasSuffix(value, "#wechat_redirect") {
		t.Fatalf("url = %q", value)
	}
	for _, part := range []string{
		"appid=wx-test-app",
		"response_type=code",
		"scope=snsapi_base",
		"state=state-1",
		"redirect_uri=https%3A%2F%2Fexample.com%2Fcb",
	} {
		if !strings.Contains(value, part) {
			t.Fatalf("url %q missing %q", value, part)
		}
	}
}

func TestSignature(t *testing.T) {
	client := newTestClient(t, KindOfficial, "")
	timestamp := "1409304348"
	nonce := "nonce"
	signature := Sign("test-token", timestamp, nonce)
	if !client.VerifySignature(signature, timestamp, nonce) {
		t.Fatal("signature verification failed")
	}
	if client.VerifySignature(signature, timestamp, "other") {
		t.Fatal("signature should not match")
	}

	encrypt := "cipher"
	messageSignature := SignMessage("test-token", timestamp, nonce, encrypt)
	if !client.VerifyMessageSignature(messageSignature, timestamp, nonce, encrypt) {
		t.Fatal("message signature verification failed")
	}
}

func TestDecryptMessage(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	aesKey := strings.TrimRight(base64.StdEncoding.EncodeToString(key), "=")
	app := testApp(KindOfficial)
	app.EncodingAESKey = aesKey
	client, err := New(app)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	message := []byte("<xml>hello</xml>")
	plaintext := make([]byte, 16)
	plaintext = append(plaintext, make([]byte, 4)...)
	binary.BigEndian.PutUint32(plaintext[16:20], uint32(len(message)))
	plaintext = append(plaintext, message...)
	plaintext = append(plaintext, []byte(app.AppID)...)
	encrypted := encryptCBC(key, key[:aes.BlockSize], pkcs7Pad(plaintext, 32))
	encoded := base64.StdEncoding.EncodeToString(encrypted)

	decrypted, err := client.DecryptMessage(encoded)
	if err != nil {
		t.Fatalf("DecryptMessage: %v", err)
	}
	if string(decrypted) != string(message) {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestDecryptPhone(t *testing.T) {
	sessionKey := []byte("0123456789abcdef")
	iv := []byte("abcdef0123456789")
	payload := []byte(`{"phoneNumber":"13800138000","purePhoneNumber":"13800138000","countryCode":"86"}`)
	encrypted := encryptCBC(sessionKey, iv, pkcs7Pad(payload, aes.BlockSize))

	client := newTestClient(t, KindMini, "")
	phone, err := client.DecryptPhone(
		base64.StdEncoding.EncodeToString(sessionKey),
		base64.StdEncoding.EncodeToString(encrypted),
		base64.StdEncoding.EncodeToString(iv),
	)
	if err != nil {
		t.Fatalf("DecryptPhone: %v", err)
	}
	if phone.PurePhoneNumber != "13800138000" {
		t.Fatalf("phone = %+v", phone)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(AppConfig{Name: "x", Type: "unknown", AppID: "a", AppSecret: "b"}); err == nil {
		t.Fatal("expected invalid type error")
	}
	if _, err := New(AppConfig{Name: "x", Type: "mini", AppID: "", AppSecret: "b"}); err == nil {
		t.Fatal("expected missing app id error")
	}
	app := testApp(KindMini)
	app.EncodingAESKey = "invalid"
	if _, err := New(app); err == nil {
		t.Fatal("expected invalid aes key error")
	}
}

func TestAccessTokenCached(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/token" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"access_token":"ACCESS","expires_in":7200}`))
	}))
	defer server.Close()

	client := newTestClient(t, KindMini, server.URL)
	first, err := client.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	second, err := client.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if first != "ACCESS" || second != "ACCESS" {
		t.Fatalf("tokens = %q, %q", first, second)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", got)
	}
}

func TestCode2SessionAndPhone(t *testing.T) {
	var tokenCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			atomic.AddInt32(&tokenCalls, 1)
			_, _ = w.Write([]byte(`{"access_token":"ACCESS","expires_in":7200}`))
		case "/sns/jscode2session":
			if r.URL.Query().Get("js_code") != "code-1" {
				t.Errorf("js_code = %q", r.URL.Query().Get("js_code"))
			}
			_, _ = w.Write([]byte(`{"openid":"OPENID","session_key":"SESSION","unionid":"UNION"}`))
		case "/wxa/business/getuserphonenumber":
			if r.URL.Query().Get("access_token") != "ACCESS" {
				t.Errorf("access_token = %q", r.URL.Query().Get("access_token"))
			}
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","phone_info":{"phoneNumber":"13800138000","purePhoneNumber":"13800138000","countryCode":"86"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(t, KindMini, server.URL)
	session, err := client.Code2Session(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("Code2Session: %v", err)
	}
	if session.OpenID != "OPENID" || session.SessionKey != "SESSION" || session.UnionID != "UNION" {
		t.Fatalf("session = %+v", session)
	}

	phone, err := client.PhoneNumber(context.Background(), "phone-code")
	if err != nil {
		t.Fatalf("PhoneNumber: %v", err)
	}
	if phone.PurePhoneNumber != "13800138000" {
		t.Fatalf("phone = %+v", phone)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token calls = %d", tokenCalls)
	}
}

func TestKindGuard(t *testing.T) {
	mini := newTestClient(t, KindMini, "")
	if _, err := mini.ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("mini program must not use official oauth")
	}
	official := newTestClient(t, KindOfficial, "")
	if _, err := official.Code2Session(context.Background(), "code"); err == nil {
		t.Fatal("official account must not use mini code2session")
	}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytesRepeat(byte(padding), padding)...)
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}

func encryptCBC(key, iv, plaintext []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)
	return ciphertext
}
