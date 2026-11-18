package crypto

import (
	"bytes"
	crand "crypto/rand"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func setConfig(t *testing.T, cfg Config) {
	t.Helper()
	old := *GetConfig()
	*GetConfig() = cfg
	t.Cleanup(func() {
		*GetConfig() = old
		Set(nil)
	})
}

func TestConfigGetSet(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	encryptor, err := NewAESGCM("primary", key)
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	Set(encryptor)
	if Get() != encryptor {
		t.Fatal("Get should return the configured encryptor")
	}
	Set(nil)
	if Get() != nil {
		t.Fatal("Get should return nil after Set(nil)")
	}
}

func TestStartupDisabledWithoutKey(t *testing.T) {
	setConfig(t, DefaultConfig())
	if err := Startup(t.Context()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected encryptor to stay disabled without key")
	}
}

func TestStartupUnsupportedAlgorithm(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Algorithm = "rsa"
	setConfig(t, cfg)
	if err := Startup(t.Context()); err == nil {
		t.Fatal("expected unsupported algorithm error")
	}
}

func TestStartupInvalidKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Key = "not-base64"
	setConfig(t, cfg)
	if err := Startup(t.Context()); err == nil {
		t.Fatal("expected invalid key error")
	}
}

func TestStartupBase64Key(t *testing.T) {
	key := bytes.Repeat([]byte{0x22}, 32)
	cfg := DefaultConfig()
	cfg.Key = base64.StdEncoding.EncodeToString(key)
	setConfig(t, cfg)

	if err := Startup(t.Context()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	encryptor := Get()
	if encryptor == nil {
		t.Fatal("expected encryptor to be configured")
	}
	ciphertext, err := encryptor.Encrypt([]byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil || string(plaintext) != "payload" {
		t.Fatalf("Decrypt = %q, %v", plaintext, err)
	}

	if err := Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if Get() != nil {
		t.Fatal("expected encryptor to be released")
	}
}

func TestStartupRawKeyFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Key = strings.Repeat("k", 32)
	setConfig(t, cfg)

	if err := Startup(t.Context()); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if Get() == nil {
		t.Fatal("expected raw 32-byte key to be accepted")
	}
}

func TestStartupRejectsLongKeyID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Key = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	cfg.KeyID = strings.Repeat("x", 256)
	setConfig(t, cfg)

	if err := Startup(t.Context()); err == nil {
		t.Fatal("expected long key id error")
	}
}

func TestEncryptNonceFailure(t *testing.T) {
	encryptor, err := NewAESGCM("primary", bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	original := crand.Reader
	crand.Reader = errorReader{}
	defer func() { crand.Reader = original }()

	if _, err := encryptor.Encrypt([]byte("x")); err == nil {
		t.Fatal("expected nonce generation error")
	}
}

func TestEncryptInvalidKey(t *testing.T) {
	encryptor := &AESGCM{keyID: "primary", key: []byte("short")}
	if _, err := encryptor.Encrypt([]byte("x")); err == nil {
		t.Fatal("expected cipher creation error")
	}
}

func envelope(keyID string, payload []byte) []byte {
	out := []byte{envelopeVersion, byte(len(keyID))}
	out = append(out, keyID...)
	return append(out, payload...)
}

func TestDecryptMalformedEnvelopes(t *testing.T) {
	encryptor, err := NewAESGCM("primary", bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	cases := map[string][]byte{
		"too short":        {},
		"single byte":      {envelopeVersion},
		"bad version":      {0x02, 0x00},
		"truncated key id": {envelopeVersion, 0x05, 'p'},
		"short nonce":      envelope("primary", []byte{1, 2, 3}),
		"empty data":       envelope("primary", make([]byte, 12)),
	}
	for name, ciphertext := range cases {
		if _, err := encryptor.Decrypt(ciphertext); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}

	invalid := &AESGCM{keyID: "primary", key: []byte("short")}
	if _, err := invalid.Decrypt(envelope("primary", make([]byte, 16))); err == nil {
		t.Fatal("expected cipher creation error")
	}
}

func TestDecryptWrongKeySameID(t *testing.T) {
	first, err := NewAESGCM("primary", bytes.Repeat([]byte{0x66}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	second, err := NewAESGCM("primary", bytes.Repeat([]byte{0x77}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	ciphertext, err := first.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := second.Decrypt(ciphertext); err == nil {
		t.Fatal("expected decryption failure with a different key")
	}
}

func TestEncryptDecryptEmptyPayload(t *testing.T) {
	encryptor, err := NewAESGCM("", bytes.Repeat([]byte{0x88}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	ciphertext, err := encryptor.Encrypt(nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil || len(plaintext) != 0 {
		t.Fatalf("Decrypt = %v, %v", plaintext, err)
	}
}
