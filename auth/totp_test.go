package auth

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

const rfc6238Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		want string
	}{
		{unix: 59, want: "94287082"},
		{unix: 1111111109, want: "07081804"},
		{unix: 1111111111, want: "14050471"},
		{unix: 1234567890, want: "89005924"},
		{unix: 2000000000, want: "69279037"},
		{unix: 20000000000, want: "65353130"},
	}

	for _, tc := range cases {
		got, err := TOTPCode(rfc6238Secret, time.Unix(tc.unix, 0), 8, 30)
		if err != nil {
			t.Fatalf("TOTPCode(%d): %v", tc.unix, err)
		}
		if got != tc.want {
			t.Fatalf("TOTPCode(%d) = %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	at := time.Unix(1111111111, 0)
	code, err := TOTPCode(rfc6238Secret, at, 6, 30)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d, want 6", len(code))
	}

	if !VerifyTOTP(rfc6238Secret, code, at, 6, 30, 0) {
		t.Fatal("expected current code to verify")
	}
	if !VerifyTOTP(rfc6238Secret, " "+code+" ", at, 6, 30, 0) {
		t.Fatal("expected trimmed code to verify")
	}
	if !VerifyTOTP(rfc6238Secret, code, at.Add(30*time.Second), 6, 30, 1) {
		t.Fatal("expected adjacent period to verify with skew 1")
	}
	if !VerifyTOTP(rfc6238Secret, code, at.Add(-30*time.Second), 6, 30, 1) {
		t.Fatal("expected previous period to verify with skew 1")
	}
	if VerifyTOTP(rfc6238Secret, code, at.Add(60*time.Second), 6, 30, 1) {
		t.Fatal("expected code outside skew window to be rejected")
	}
	if VerifyTOTP(rfc6238Secret, "000000", at, 6, 30, 0) {
		t.Fatal("expected wrong code to be rejected")
	}
	if VerifyTOTP("not-base32!", code, at, 6, 30, 0) {
		t.Fatal("expected invalid secret to be rejected")
	}
	if !VerifyTOTP(rfc6238Secret, code, at, 0, 0, 0) {
		t.Fatal("expected zero digits/period to use defaults")
	}
}

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	if len(raw) != 20 {
		t.Fatalf("secret length = %d, want 20", len(raw))
	}

	another, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if secret == another {
		t.Fatal("expected generated secrets to differ")
	}
}

func TestTOTPCodeErrors(t *testing.T) {
	if _, err := TOTPCode("", time.Now(), 6, 30); err == nil {
		t.Fatal("expected empty secret to be rejected")
	}
	if _, err := TOTPCode("!!!", time.Now(), 6, 30); err == nil {
		t.Fatal("expected invalid secret to be rejected")
	}
	if _, err := TOTPCode(rfc6238Secret, time.Unix(-1, 0), 6, 30); err == nil {
		t.Fatal("expected negative time to be rejected")
	}
	if _, err := TOTPCode(rfc6238Secret, time.Now(), 11, 30); err == nil {
		t.Fatal("expected too many digits to be rejected")
	}
}

func TestOTPAuthURL(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	raw := OTPAuthURL("Goframe", "alice@example.com", secret, 6, 30)
	if !strings.HasPrefix(raw, "otpauth://totp/") {
		t.Fatalf("unexpected url: %s", raw)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Host != "totp" {
		t.Fatalf("host = %s, want totp", parsed.Host)
	}
	if !strings.Contains(parsed.Path, "Goframe") || !strings.Contains(parsed.Path, "alice@example.com") {
		t.Fatalf("unexpected label path: %s", parsed.Path)
	}

	query := parsed.Query()
	if query.Get("secret") != secret {
		t.Fatalf("secret = %s, want %s", query.Get("secret"), secret)
	}
	if query.Get("issuer") != "Goframe" {
		t.Fatalf("issuer = %s, want Goframe", query.Get("issuer"))
	}
	if query.Get("algorithm") != "SHA1" || query.Get("digits") != "6" || query.Get("period") != "30" {
		t.Fatalf("unexpected query: %v", query)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(5)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 5 {
		t.Fatalf("len(codes) = %d, want 5", len(codes))
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if len(code) != 9 || code[4] != '-' {
			t.Fatalf("unexpected recovery code format: %s", code)
		}
		normalized := NormalizeRecoveryCode(code)
		if len(normalized) != 8 {
			t.Fatalf("normalized length = %d, want 8", len(normalized))
		}
		if strings.Trim(normalized, recoveryAlphabet) != "" {
			t.Fatalf("recovery code %s contains invalid characters", normalized)
		}
		if seen[normalized] {
			t.Fatalf("duplicate recovery code: %s", normalized)
		}
		seen[normalized] = true
	}

	if _, err := GenerateRecoveryCodes(0); err == nil {
		t.Fatal("expected zero count to be rejected")
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	cases := map[string]string{
		" abcd-efgh ": "ABCDEFGH",
		"AB CD_EF-GH": "ABCDEFGH",
		"ab12cd34":    "AB12CD34",
		"----":        "",
	}
	for input, want := range cases {
		if got := NormalizeRecoveryCode(input); got != want {
			t.Fatalf("NormalizeRecoveryCode(%q) = %q, want %q", input, got, want)
		}
	}
}
