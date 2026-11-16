package crypto

import (
	"bytes"
	"testing"
)

func TestAESGCMEncryptDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{0x2a}, 32)
	encryptor, err := NewAESGCM("primary", key)
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	plaintext := []byte("sensitive payload")
	ciphertext, err := encryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext must not contain plaintext")
	}

	decrypted, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plaintext)
	}

	ciphertext[len(ciphertext)-1] ^= 0xff
	if _, err := encryptor.Decrypt(ciphertext); err == nil {
		t.Fatal("expected tampered ciphertext to be rejected")
	}
}

func TestAESGCMRejectsInvalidKey(t *testing.T) {
	if _, err := NewAESGCM("primary", []byte("short")); err == nil {
		t.Fatal("expected short key to be rejected")
	}
}

func TestAESGCMRejectsUnknownKeyID(t *testing.T) {
	key := bytes.Repeat([]byte{0x01}, 32)
	first, err := NewAESGCM("primary", key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAESGCM("secondary", key)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := first.Encrypt([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Decrypt(ciphertext); err == nil {
		t.Fatal("expected unknown key id to be rejected")
	}
}
