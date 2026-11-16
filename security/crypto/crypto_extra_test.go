package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNewKeyFromBase64(t *testing.T) {
	key := bytes.Repeat([]byte{0x7f}, 32)
	encoded := base64.StdEncoding.EncodeToString(key)

	decoded, err := NewKeyFromBase64(encoded)
	if err != nil {
		t.Fatalf("NewKeyFromBase64: %v", err)
	}
	if !bytes.Equal(decoded, key) {
		t.Fatal("decoded key mismatch")
	}

	if _, err := NewKeyFromBase64("not-base64!"); err == nil {
		t.Fatal("expected invalid base64 error")
	}
	if _, err := NewKeyFromBase64(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected invalid key length error")
	}
}

func TestKeyID(t *testing.T) {
	encryptor, err := NewAESGCM("primary", bytes.Repeat([]byte{0x01}, 32))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	if encryptor.KeyID() != "primary" {
		t.Fatalf("key id = %q", encryptor.KeyID())
	}
	if _, err := NewAESGCM(string(bytes.Repeat([]byte("x"), 256)), bytes.Repeat([]byte{0x01}, 32)); err == nil {
		t.Fatal("expected long key id to be rejected")
	}
}
