package csrf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func testConfig() Config {
	return Config{
		Enabled:    true,
		CookieName: "csrf_token",
		HeaderName: "X-CSRF-Token",
		TTL:        time.Hour,
	}
}

func issueToken(t *testing.T, protector *Protector) (string, *http.Cookie) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	token, err := protector.Token(c)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected csrf cookie to be set")
	}
	return token, cookies[0]
}

func validate(t *testing.T, protector *Protector, cookie *http.Cookie, token string) error {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/submit", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if token != "" {
		req.Header.Set(protector.HeaderName(), token)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return protector.Validate(c)
}

func TestCSRFTokenAndValidate(t *testing.T) {
	protector, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	token, cookie := issueToken(t, protector)
	if !strings.Contains(token, ".") {
		t.Fatalf("unexpected token format: %s", token)
	}
	if cookie.Name != "csrf_token" || !cookie.HttpOnly {
		t.Fatalf("unexpected cookie: %+v", cookie)
	}
	if err := validate(t, protector, cookie, token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	protector, _ := New(testConfig())
	_, cookie := issueToken(t, protector)

	if err := validate(t, protector, cookie, ""); err == nil {
		t.Fatal("expected missing header to be rejected")
	}
	if err := validate(t, protector, nil, ""); err == nil {
		t.Fatal("expected missing cookie to be rejected")
	}
}

func TestCSRFRejectsTamperedToken(t *testing.T) {
	protector, _ := New(testConfig())
	token, cookie := issueToken(t, protector)

	if err := validate(t, protector, cookie, token+"tampered"); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}

	second, _ := issueToken(t, protector)
	if err := validate(t, protector, cookie, second); err == nil {
		t.Fatal("expected cookie/header mismatch to be rejected")
	}
}

func TestCSRFExpiredToken(t *testing.T) {
	cfg := testConfig()
	cfg.TTL = 5 * time.Millisecond
	protector, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, cookie := issueToken(t, protector)
	time.Sleep(20 * time.Millisecond)

	if err := validate(t, protector, cookie, token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestCSRFGeneratesSecretWhenMissing(t *testing.T) {
	cfg := testConfig()
	cfg.Secret = ""
	protector, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, cookie := issueToken(t, protector)
	if err := validate(t, protector, cookie, token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
