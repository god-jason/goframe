package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVaultProviderGetAndPut(t *testing.T) {
	var stored map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"data": stored},
			})
		case http.MethodPost:
			var payload struct {
				Data map[string]string `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			stored = payload.Data
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	provider, err := NewVault(VaultConfig{Address: server.URL, Token: "token"})
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	if err := provider.Put(context.Background(), "app/key", map[string]string{"value": "secret"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	value, err := provider.Get(context.Background(), "app/key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value != "secret" {
		t.Fatalf("value = %q", value)
	}
}

func TestVaultProviderValidation(t *testing.T) {
	if _, err := NewVault(VaultConfig{}); err == nil {
		t.Fatal("expected address error")
	}
	if _, err := NewVault(VaultConfig{Address: "http://localhost:8200"}); err == nil {
		t.Fatal("expected token error")
	}
}

func TestRotatingProvider(t *testing.T) {
	ctx := context.Background()
	provider := NewRotating(time.Minute)
	provider.Set("jwt", "v1")

	now := time.Now()
	provider.now = func() time.Time { return now }

	if !provider.Verify("jwt", "v1") {
		t.Fatal("current value should verify")
	}
	value, err := provider.Rotate(ctx, "jwt", func(context.Context) (string, error) { return "v2", nil })
	if err != nil || value != "v2" {
		t.Fatalf("Rotate = %q, %v", value, err)
	}
	if !provider.Verify("jwt", "v1") {
		t.Fatal("previous value should verify during grace")
	}

	now = now.Add(2 * time.Minute)
	provider.Cleanup()
	if provider.Verify("jwt", "v1") {
		t.Fatal("previous value should expire")
	}
	if previous, _ := provider.Previous(ctx, "jwt"); len(previous) != 0 {
		t.Fatalf("previous = %v, want empty", previous)
	}
}
