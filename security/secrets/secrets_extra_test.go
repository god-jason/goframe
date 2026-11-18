package secrets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnvProviderEmptyValue(t *testing.T) {
	t.Setenv("GOF_EMPTY_VALUE", "")
	provider := NewEnv("GOF_")
	if _, err := provider.Get(context.Background(), "EMPTY_VALUE"); err == nil {
		t.Fatal("expected empty value to be treated as missing")
	}
}

func TestNewVaultProviderFromEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "token" || r.Header.Get("X-Vault-Namespace") != "ns" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !strings.Contains(r.URL.Path, "/v1/kv/data/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"data":{"value":"v"}}}`))
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "token")
	t.Setenv("VAULT_NAMESPACE", "ns")
	t.Setenv("VAULT_MOUNT", "kv/")

	provider, err := New("vault")
	if err != nil {
		t.Fatalf("New vault: %v", err)
	}
	value, err := provider.Get(context.Background(), "app/key")
	if err != nil || value != "v" {
		t.Fatalf("Get = %q, %v", value, err)
	}

	if _, err := New("unknown"); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestVaultGetFieldSelection(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "value field", body: `{"data":{"data":{"value":"v","other":"x"}}}`, want: "v"},
		{name: "single field", body: `{"data":{"data":{"only":"v"}}}`, want: "v"},
		{name: "multiple without value", body: `{"data":{"data":{"a":"1","b":"2"}}}`, wantErr: true},
		{name: "empty object", body: `{"data":{"data":{}}}`, wantErr: true},
		{name: "null data", body: `{"data":{}}`, wantErr: true},
		{name: "decode error", body: `not-json`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			provider, err := NewVault(VaultConfig{Address: server.URL, Token: "token"})
			if err != nil {
				t.Fatalf("NewVault: %v", err)
			}
			value, err := provider.Get(context.Background(), "app/key")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || value != tc.want {
				t.Fatalf("Get = %q, %v; want %q", value, err, tc.want)
			}
		})
	}
}

func TestVaultStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer server.Close()

	provider, err := NewVault(VaultConfig{Address: server.URL, Token: "token"})
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	if _, err := provider.Get(context.Background(), "app/key"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Get err = %v, want status error", err)
	}
	if err := provider.Put(context.Background(), "app/key", map[string]string{"value": "v"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Put err = %v, want status error", err)
	}
}

func TestVaultRequestErrors(t *testing.T) {
	provider, err := NewVault(VaultConfig{Address: "http://127.0.0.1:1", Token: "token"})
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Put(ctx, "app/key", map[string]string{"value": "v"}); err == nil {
		t.Fatal("expected write error")
	}
	if _, err := provider.Get(ctx, "app/key"); err == nil {
		t.Fatal("expected read error")
	}

	// 名称中的换行会让 URL 非法，覆盖请求构造失败分支。
	if _, err := provider.Get(context.Background(), "bad\nname"); err == nil {
		t.Fatal("expected request creation error")
	}
	if err := provider.Put(context.Background(), "bad\nname", map[string]string{"value": "v"}); err == nil {
		t.Fatal("expected request creation error")
	}
}

func TestRotatingProviderGet(t *testing.T) {
	ctx := context.Background()
	provider := NewRotating(time.Minute)
	provider.Set("jwt", "v1")

	if value, err := provider.Get(ctx, "jwt"); err != nil || value != "v1" {
		t.Fatalf("Get = %q, %v", value, err)
	}
	if _, err := provider.Get(ctx, "missing"); err == nil {
		t.Fatal("expected missing secret error")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := provider.Get(canceled, "jwt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRotatingProviderDefaults(t *testing.T) {
	if provider := NewRotating(0); provider.grace != time.Hour {
		t.Fatalf("grace = %v, want 1h", provider.grace)
	}
}

func TestRotatingProviderRotateErrors(t *testing.T) {
	ctx := context.Background()
	provider := NewRotating(time.Minute)

	if _, err := provider.Rotate(ctx, "jwt", nil); err == nil {
		t.Fatal("expected nil generator error")
	}
	if _, err := provider.Rotate(ctx, "jwt", func(context.Context) (string, error) {
		return "", errors.New("boom")
	}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want generator error", err)
	}
	if _, err := provider.Rotate(ctx, "jwt", func(context.Context) (string, error) {
		return "", nil
	}); err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Fatalf("err = %v, want empty value error", err)
	}

	// 首次轮换没有旧值，Previous 应为空。
	value, err := provider.Rotate(ctx, "new", func(context.Context) (string, error) { return "v1", nil })
	if err != nil || value != "v1" {
		t.Fatalf("Rotate = %q, %v", value, err)
	}
	if previous, err := provider.Previous(ctx, "new"); err != nil || len(previous) != 0 {
		t.Fatalf("Previous = %v, %v", previous, err)
	}
}

func TestRotatingProviderPreviousAndGrace(t *testing.T) {
	ctx := context.Background()
	provider := NewRotating(time.Minute)
	provider.Set("jwt", "v1")

	now := time.Now()
	provider.now = func() time.Time { return now }

	if previous, err := provider.Previous(ctx, "jwt"); err != nil || len(previous) != 0 {
		t.Fatalf("Previous before rotate = %v, %v", previous, err)
	}

	if _, err := provider.Rotate(ctx, "jwt", func(context.Context) (string, error) { return "v2", nil }); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	previous, err := provider.Previous(ctx, "jwt")
	if err != nil || len(previous) != 1 || previous[0] != "v1" {
		t.Fatalf("Previous = %v, %v", previous, err)
	}
	if !provider.Verify("jwt", "v2") {
		t.Fatal("current value should verify")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := provider.Previous(canceled, "jwt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	now = now.Add(2 * time.Minute)
	if previous, err := provider.Previous(ctx, "jwt"); err != nil || len(previous) != 0 {
		t.Fatalf("expired Previous = %v, %v", previous, err)
	}
	if provider.Verify("jwt", "v1") {
		t.Fatal("expired previous should not verify")
	}

	provider.Set("jwt", "v3")
	if provider.Verify("jwt", "v1") || provider.Verify("jwt", "v2") {
		t.Fatal("Set should discard previous value")
	}
}

func TestRotatingProviderConcurrentAccess(t *testing.T) {
	provider := NewRotating(time.Minute)
	provider.Set("jwt", "v0")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = provider.Get(context.Background(), "jwt")
			}
		}()
		go func() {
			defer wg.Done()
			_, _ = provider.Rotate(context.Background(), "jwt", func(context.Context) (string, error) { return "v", nil })
		}()
		go func() {
			defer wg.Done()
			_ = provider.Verify("jwt", "v")
		}()
		go func() {
			defer wg.Done()
			_, _ = provider.Previous(context.Background(), "jwt")
			provider.Cleanup()
		}()
	}
	wg.Wait()
}
