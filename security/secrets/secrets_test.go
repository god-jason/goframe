package secrets

import (
	"context"
	"testing"
)

func TestEnvProvider(t *testing.T) {
	t.Setenv("GOF_TEST_TOKEN", "secret-value")

	provider := NewEnv("GOF_TEST_")
	value, err := provider.Get(context.Background(), "TOKEN")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("value = %q", value)
	}

	if _, err := provider.Get(context.Background(), "MISSING"); err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestNewProvider(t *testing.T) {
	if _, err := New(""); err != nil {
		t.Fatalf("New empty provider: %v", err)
	}
	if _, err := New("env"); err != nil {
		t.Fatalf("New env provider: %v", err)
	}
	if _, err := New("vault"); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestEnvProviderCanceledContext(t *testing.T) {
	provider := NewEnv("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Get(ctx, "ANY"); err == nil {
		t.Fatal("expected canceled context error")
	}
}
