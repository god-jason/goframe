package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/auth"
	"github.com/god-jason/goframe/permission"
	"github.com/god-jason/goframe/security/audit"
	"github.com/god-jason/goframe/security/csrf"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type fakeHasher struct {
	hashErr   error
	verifyErr error
	ok        bool
}

func (f fakeHasher) Hash(string) (string, error) {
	if f.hashErr != nil {
		return "", f.hashErr
	}
	return "hashed", nil
}

func (f fakeHasher) Verify(string, string) (bool, error) {
	return f.ok, f.verifyErr
}

type fakeRepository struct {
	user *User
	err  error
}

func (f *fakeRepository) GetByUsername(context.Context, string) (*User, error) {
	return f.user, f.err
}

func (f *fakeRepository) Create(context.Context, *User) error { return f.err }

type fakeManager struct {
	loginErr   error
	refreshErr error
	logoutErr  error
}

func (f *fakeManager) Authenticate(context.Context, string) (*auth.Identity, error) {
	return nil, nil
}

func (f *fakeManager) Login(context.Context, *auth.Identity, auth.SessionMeta) (*auth.Tokens, *auth.Identity, error) {
	return nil, nil, f.loginErr
}

func (f *fakeManager) Refresh(context.Context, string, auth.SessionMeta) (*auth.Tokens, *auth.Identity, error) {
	return nil, nil, f.refreshErr
}

func (f *fakeManager) Logout(context.Context, string) error { return f.logoutErr }

func (f *fakeManager) ListSessions(context.Context, string) ([]*auth.Session, error) {
	return nil, nil
}

func (f *fakeManager) RevokeSession(context.Context, string, string) error { return nil }

func (f *fakeManager) RevokeAllSessions(context.Context, string, string) error { return nil }

type fakeAuditor struct {
	err error
}

func (f *fakeAuditor) Record(context.Context, audit.Event) error { return f.err }

func testHasher(t *testing.T) auth.PasswordHasher {
	t.Helper()
	hasher, err := auth.NewPasswordHasher(auth.PasswordConfig{
		Memory:      8 * 1024,
		Iterations:  1,
		Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("new password hasher: %v", err)
	}
	return hasher
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	service := NewService(NewMemoryUserRepository(), testHasher(t))
	if err := service.CreateUser(context.Background(), "1", DefaultUsername, DefaultPassword,
		[]string{"admin"}, []string{"system:read", "user:read"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	manager, err := auth.NewService(auth.DefaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	return NewHandler(service, manager, permission.Get())
}

func newTestContext(method, target string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(method, target, body)
	return c, writer
}

func decodeTokens(t *testing.T, body []byte) (access, refresh string) {
	t.Helper()
	var payload struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode tokens: %v, body = %s", err, body)
	}
	return payload.Data.AccessToken, payload.Data.RefreshToken
}

func TestModuleInitAndRoutes(t *testing.T) {
	module := New()
	if module.Name() != "demo" {
		t.Fatalf("name = %q", module.Name())
	}
	if err := module.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if module.handler == nil {
		t.Fatal("handler not initialized")
	}

	router := gin.New()
	if err := module.Mount(router); err != nil {
		t.Fatalf("mount: %v", err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/ping", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("ping status = %d", recorder.Code)
	}
}

func TestModuleInitPasswordHasherError(t *testing.T) {
	original := newPasswordHasher
	t.Cleanup(func() { newPasswordHasher = original })
	newPasswordHasher = func(auth.PasswordConfig) (auth.PasswordHasher, error) {
		return nil, errors.New("hasher failed")
	}
	if err := New().Init(); err == nil {
		t.Fatal("expected hasher error")
	}
}

func TestModuleInitCreateUserError(t *testing.T) {
	original := newPasswordHasher
	t.Cleanup(func() { newPasswordHasher = original })
	newPasswordHasher = func(auth.PasswordConfig) (auth.PasswordHasher, error) {
		return fakeHasher{hashErr: errors.New("hash failed")}, nil
	}
	if err := New().Init(); err == nil {
		t.Fatal("expected create user error")
	}
}

func TestServiceCreateUserAndAuthenticate(t *testing.T) {
	ctx := context.Background()
	hasher := testHasher(t)
	service := NewService(NewMemoryUserRepository(), hasher)

	if err := service.CreateUser(ctx, "1", "admin", "secret", []string{"admin"}, []string{"user:read"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := service.CreateUser(ctx, "2", "admin", "secret", nil, nil); err == nil {
		t.Fatal("expected conflict error")
	}
	if err := service.CreateUser(ctx, "3", "empty", "", nil, nil); err == nil {
		t.Fatal("expected hash error for empty password")
	}

	identity, err := service.Authenticate(ctx, "admin", "secret")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if identity.UserID != "1" || identity.AuthType != "user" {
		t.Fatalf("identity = %+v", identity)
	}
	if _, err := service.Authenticate(ctx, "admin", "wrong"); err == nil {
		t.Fatal("expected credential error")
	}
	if _, err := service.Authenticate(ctx, "missing", "secret"); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestServiceErrorPaths(t *testing.T) {
	ctx := context.Background()
	hasher := testHasher(t)

	repoErr := errors.New("db unavailable")
	if err := NewService(&fakeRepository{err: repoErr}, hasher).CreateUser(ctx, "1", "u", "p", nil, nil); !errors.Is(err, repoErr) {
		t.Fatalf("create user error = %v", err)
	}
	if _, err := NewService(&fakeRepository{err: repoErr}, hasher).Authenticate(ctx, "u", "p"); err == nil {
		t.Fatal("expected authenticate error")
	}

	verifyErr := errors.New("verify failed")
	service := NewService(&fakeRepository{user: &User{ID: "1", PasswordHash: "x"}}, fakeHasher{verifyErr: verifyErr})
	if _, err := service.Authenticate(ctx, "u", "p"); err == nil {
		t.Fatal("expected verify error")
	}
	service = NewService(&fakeRepository{user: &User{ID: "1", PasswordHash: "x"}}, fakeHasher{ok: false})
	if _, err := service.Authenticate(ctx, "u", "p"); err == nil {
		t.Fatal("expected verify mismatch error")
	}
}

func TestMemoryUserRepository(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryUserRepository()

	if _, err := repo.GetByUsername(ctx, "missing"); err == nil {
		t.Fatal("expected not found error")
	}
	user := &User{ID: "1", Username: "admin", Roles: []string{"admin"}, Permissions: []string{"user:read"}}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Create(ctx, user); err == nil {
		t.Fatal("expected conflict error")
	}

	stored, err := repo.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Roles[0] = "mutated"
	again, err := repo.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.Roles[0] != "admin" {
		t.Fatal("repository returned shared slice")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.GetByUsername(canceled, "admin"); err == nil {
		t.Fatal("expected canceled context error")
	}
	if err := repo.Create(canceled, user); err == nil {
		t.Fatal("expected canceled context error")
	}

	var nilUser *User
	if nilUser.clone() != nil {
		t.Fatal("clone of nil user should be nil")
	}
}

func TestHandlerRoutes(t *testing.T) {
	handler := newTestHandler(t)
	router := gin.New()
	if err := handler.Mount(router); err != nil {
		t.Fatalf("mount: %v", err)
	}

	do := func(method, path, body, token string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		request := httptest.NewRequest(method, path, reader)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Device-Name", "test-device")
		request.Header.Set("User-Agent", "demo-test")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	if code := do(http.MethodGet, "/public/ping", "", "").Code; code != http.StatusOK {
		t.Fatalf("ping = %d", code)
	}
	if code := do(http.MethodGet, "/public/csrf-token", "", "").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("csrf disabled = %d", code)
	}
	if code := do(http.MethodPost, "/auth/login", "{", "").Code; code != http.StatusBadRequest {
		t.Fatalf("login invalid json = %d", code)
	}
	if code := do(http.MethodPost, "/auth/login", `{"username":"admin"}`, "").Code; code != http.StatusBadRequest {
		t.Fatalf("login missing password = %d", code)
	}
	if code := do(http.MethodPost, "/auth/login", `{"username":"admin","password":"wrong"}`, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("login wrong password = %d", code)
	}

	recorder := do(http.MethodPost, "/auth/login", `{"username":"admin","password":"`+DefaultPassword+`"}`, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("login = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	access, refresh := decodeTokens(t, recorder.Body.Bytes())
	if access == "" || refresh == "" {
		t.Fatalf("tokens = %q / %q", access, refresh)
	}

	if code := do(http.MethodGet, "/user/me", "", access).Code; code != http.StatusOK {
		t.Fatalf("me = %d", code)
	}
	if code := do(http.MethodGet, "/system/info", "", "").Code; code != http.StatusUnauthorized {
		t.Fatalf("system info unauthenticated = %d", code)
	}
	systemInfo := do(http.MethodGet, "/system/info", "", access)
	if systemInfo.Code != http.StatusOK {
		t.Fatalf("system info = %d", systemInfo.Code)
	}
	if !strings.Contains(systemInfo.Body.String(), "go_version") {
		t.Fatalf("system info body = %s", systemInfo.Body.String())
	}

	if code := do(http.MethodPost, "/auth/refresh", "{", "").Code; code != http.StatusBadRequest {
		t.Fatalf("refresh invalid json = %d", code)
	}
	if code := do(http.MethodPost, "/auth/refresh", `{"refresh_token":"invalid"}`, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("refresh invalid token = %d", code)
	}
	recorder = do(http.MethodPost, "/auth/refresh", `{"refresh_token":"`+refresh+`"}`, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	access, _ = decodeTokens(t, recorder.Body.Bytes())

	if code := do(http.MethodPost, "/auth/logout", "", access).Code; code != http.StatusNoContent {
		t.Fatalf("logout = %d", code)
	}
}

func TestHandlerCSRFProtection(t *testing.T) {
	handler := newTestHandler(t)
	protector, err := csrf.New(csrf.DefaultConfig())
	if err != nil {
		t.Fatalf("new protector: %v", err)
	}
	handler.csrf = protector

	router := gin.New()
	if err := handler.Mount(router); err != nil {
		t.Fatalf("mount: %v", err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/csrf-token", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("csrf token = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), protector.HeaderName()) {
		t.Fatalf("csrf body = %s", recorder.Body.String())
	}
	if len(recorder.Result().Cookies()) == 0 {
		t.Fatal("csrf cookie not set")
	}
}

func TestHandlerDisabledAuthentication(t *testing.T) {
	handler := newTestHandler(t)
	handler.auth = nil

	loginContext, loginWriter := newTestContext(http.MethodPost, "/auth/login",
		strings.NewReader(`{"username":"admin","password":"`+DefaultPassword+`"}`))
	handler.login(loginContext)
	if loginWriter.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without auth = %d", loginWriter.Code)
	}

	refreshContext, refreshWriter := newTestContext(http.MethodPost, "/auth/refresh",
		strings.NewReader(`{"refresh_token":"token"}`))
	handler.refresh(refreshContext)
	if refreshWriter.Code != http.StatusServiceUnavailable {
		t.Fatalf("refresh without auth = %d", refreshWriter.Code)
	}

	logoutContext, logoutWriter := newTestContext(http.MethodPost, "/auth/logout", strings.NewReader(""))
	handler.logout(logoutContext)
	if logoutWriter.Code != http.StatusNoContent {
		t.Fatalf("logout without auth = %d", logoutWriter.Code)
	}
}

func TestHandlerManagerErrors(t *testing.T) {
	handler := newTestHandler(t)
	handler.auth = &fakeManager{
		loginErr:   errors.New("login failed"),
		refreshErr: errors.New("refresh failed"),
		logoutErr:  errors.New("logout failed"),
	}
	handler.auditor = nil

	router := gin.New()
	router.POST("/auth/login", handler.login)
	router.POST("/auth/refresh", handler.refresh)

	loginRequest := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"username":"admin","password":"`+DefaultPassword+`"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("login manager error = %d", loginRecorder.Code)
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "/auth/refresh",
		strings.NewReader(`{"refresh_token":"token"}`))
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshRecorder := httptest.NewRecorder()
	router.ServeHTTP(refreshRecorder, refreshRequest)
	if refreshRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("refresh manager error = %d", refreshRecorder.Code)
	}

	logoutContext, logoutWriter := newTestContext(http.MethodPost, "/auth/logout", nil)
	logoutContext.Request = logoutContext.Request.WithContext(
		auth.WithIdentity(logoutContext.Request.Context(), &auth.Identity{UserID: "1", SessionID: "session"}),
	)
	handler.logout(logoutContext)
	if logoutWriter.Code != http.StatusInternalServerError {
		t.Fatalf("logout manager error = %d", logoutWriter.Code)
	}
}

func TestHandlerDirectMethods(t *testing.T) {
	handler := newTestHandler(t)

	c, writer := newTestContext(http.MethodGet, "/user/me", nil)
	c.Request = c.Request.WithContext(auth.WithIdentity(c.Request.Context(), &auth.Identity{UserID: "1"}))
	handler.me(c)
	if writer.Code != http.StatusOK || !strings.Contains(writer.Body.String(), `"user_id":"1"`) {
		t.Fatalf("me = %d, body = %s", writer.Code, writer.Body.String())
	}

	c, writer = newTestContext(http.MethodGet, "/system/info", nil)
	handler.systemInfo(c)
	if writer.Code != http.StatusOK {
		t.Fatalf("system info = %d", writer.Code)
	}

	c, _ = newTestContext(http.MethodPost, "/auth/login", nil)
	c.Request.Header.Set("X-Device-Name", "device")
	c.Request.Header.Set("User-Agent", "agent")
	meta := handler.sessionMeta(c)
	if meta.DeviceName != "device" || meta.UserAgent != "agent" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestHandlerRecord(t *testing.T) {
	handler := newTestHandler(t)
	c, _ := newTestContext(http.MethodPost, "/auth/login", nil)

	handler.auditor = nil
	handler.record(c, "auth.login", "success", "1", nil)

	handler.auditor = &fakeAuditor{}
	handler.record(c, "auth.login", "success", "1", map[string]any{"username": "admin"})

	handler.auditor = &fakeAuditor{err: errors.New("audit failed")}
	handler.record(c, "auth.login", "success", "1", nil)
}
