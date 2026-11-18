package csrf

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setCSRFConfig(t *testing.T, cfg Config) {
	t.Helper()
	old := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = old
		_ = Shutdown(context.Background())
	})
}

func TestBootLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	disabled := DefaultConfig()
	disabled.Enabled = false
	setCSRFConfig(t, disabled)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup(disabled): %v", err)
	}
	if Get() != nil {
		t.Fatal("expected no protector when disabled")
	}

	missingCookie := DefaultConfig()
	missingCookie.CookieName = ""
	setCSRFConfig(t, missingCookie)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected missing cookie name error")
	}

	invalidTTL := DefaultConfig()
	invalidTTL.TTL = 0
	setCSRFConfig(t, invalidTTL)
	if err := Startup(context.Background()); err == nil {
		t.Fatal("expected invalid ttl error")
	}

	valid := testConfig()
	valid.Secret = "configured-secret"
	setCSRFConfig(t, valid)
	if err := Startup(context.Background()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	protector := Get()
	if protector == nil {
		t.Fatal("expected protector to be configured")
	}
	if protector.CookieName() != valid.CookieName || protector.HeaderName() != valid.HeaderName {
		t.Fatalf("unexpected protector config: %+v", protector)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected protector to be released")
	}
}

func validateToken(t *testing.T, protector *Protector, cookieValue, headerValue string) error {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/submit", nil)
	if cookieValue != "" {
		request.AddCookie(&http.Cookie{Name: protector.CookieName(), Value: cookieValue})
	}
	if headerValue != "" {
		request.Header.Set(protector.HeaderName(), headerValue)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	return protector.Validate(c)
}

func signedToken(protector *Protector, raw []byte) string {
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + protector.sign(payload)
}

func TestValidateMalformedTokens(t *testing.T) {
	protector, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	invalidBase64 := "!!!." + protector.sign("!!!")
	shortPayload := signedToken(protector, []byte("abc"))

	cases := []struct {
		name  string
		token string
	}{
		{name: "no separator", token: "abc"},
		{name: "leading separator", token: ".abc"},
		{name: "trailing separator", token: "abc."},
		{name: "bad signature", token: "abc.def"},
		{name: "invalid base64 payload", token: invalidBase64},
		{name: "short payload", token: shortPayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateToken(t, protector, tc.token, tc.token); err == nil {
				t.Fatalf("expected %q to be rejected", tc.token)
			}
		})
	}
}

func TestValidateFutureTimestamp(t *testing.T) {
	protector, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	raw := make([]byte, 24)
	binary.BigEndian.PutUint64(raw[:8], uint64(time.Now().Add(time.Minute).Unix()))
	_, _ = rand.Read(raw[8:])
	token := signedToken(protector, raw)
	if err := validateToken(t, protector, token, token); err != nil {
		t.Fatalf("Validate future token: %v", err)
	}
}

func TestTokenSecureCookie(t *testing.T) {
	cfg := testConfig()
	cfg.Secure = true
	protector, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	token, err := protector.Token(c)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected cookie flags: %+v", cookies[0])
	}
	if err := validateToken(t, protector, token, token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateEmptyCookieValue(t *testing.T) {
	protector, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, _ := issueToken(t, protector)

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.AddCookie(&http.Cookie{Name: protector.CookieName(), Value: ""})
	request.Header.Set(protector.HeaderName(), token)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	if err := protector.Validate(c); err == nil {
		t.Fatal("expected empty cookie to be rejected")
	}
}
