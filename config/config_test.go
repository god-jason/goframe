package config

import "testing"

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{
		"1024":    1024,
		"1KB":     1024,
		"1KiB":    1024,
		"10MB":    10 * 1024 * 1024,
		"1.5GB":   1536 * 1024 * 1024,
		"512 MiB": 512 * 1024 * 1024,
	}
	for input, want := range cases {
		got, err := ParseByteSize(input)
		if err != nil {
			t.Fatalf("ParseByteSize(%q): %v", input, err)
		}
		if got.Bytes() != want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", input, got, want)
		}
	}

	if _, err := ParseByteSize("-1MB"); err == nil {
		t.Fatal("expected negative size to be rejected")
	}
	if _, err := ParseByteSize("abc"); err == nil {
		t.Fatal("expected invalid size to be rejected")
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestProductionRequiresStrongSecret(t *testing.T) {
	cfg := Default()
	cfg.App.Environment = "production"
	cfg.Security.Auth.Secret = "short"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production validation to reject weak jwt secret")
	}

	cfg.Security.Auth.Secret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected strong secret to pass: %v", err)
	}
}

func TestProductionRejectsDebugMode(t *testing.T) {
	cfg := Default()
	cfg.App.Environment = "production"
	cfg.Server.Mode = "debug"
	cfg.Security.Auth.Secret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production debug mode to be rejected")
	}
}

func TestCORSWithCredentialsRejectsWildcard(t *testing.T) {
	cfg := Default()
	cfg.Security.CORS.AllowCredentials = true
	cfg.Security.CORS.AllowedOrigins = []string{"*"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected wildcard origin with credentials to be rejected")
	}
}
