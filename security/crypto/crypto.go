// Package crypto 提供对称加密能力（AES-256-GCM）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// Encryptor 是框架统一加密接口。
type Encryptor interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

const envelopeVersion byte = 0x01

// AESGCM 是基于 AES-256-GCM 的加密实现。
// 密文采用版本化 Envelope 格式：version | keyID 长度 | keyID | nonce | 密文。
type AESGCM struct {
	keyID string
	key   []byte
}

// NewAESGCM 创建 AES-256-GCM 加密器，key 必须为 32 字节。
func NewAESGCM(keyID string, key []byte) (*AESGCM, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: AES-256 requires a 32-byte key, got %d", len(key))
	}
	if len(keyID) > 255 {
		return nil, fmt.Errorf("crypto: key id too long")
	}
	return &AESGCM{keyID: keyID, key: append([]byte(nil), key...)}, nil
}

// NewKeyFromBase64 解码 Base64 编码的 32 字节密钥。
func NewKeyFromBase64(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: AES-256 requires a 32-byte key, got %d", len(key))
	}
	return key, nil
}

// KeyID 返回当前主密钥标识。
func (a *AESGCM) KeyID() string { return a.keyID }

// Encrypt 加密明文。
func (a *AESGCM) Encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := a.gcm()
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: generate nonce: %w", err)
	}

	envelope := make([]byte, 0, 2+len(a.keyID)+len(nonce)+len(plaintext)+gcm.Overhead())
	envelope = append(envelope, envelopeVersion, byte(len(a.keyID)))
	envelope = append(envelope, a.keyID...)
	envelope = append(envelope, nonce...)
	envelope = gcm.Seal(envelope, nonce, plaintext, nil)
	return envelope, nil
}

// Decrypt 解密 Envelope 格式密文。
func (a *AESGCM) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 2 {
		return nil, fmt.Errorf("crypto: ciphertext too short")
	}
	if ciphertext[0] != envelopeVersion {
		return nil, fmt.Errorf("crypto: unsupported envelope version %d", ciphertext[0])
	}
	keyIDLen := int(ciphertext[1])
	if len(ciphertext) < 2+keyIDLen {
		return nil, fmt.Errorf("crypto: malformed envelope")
	}
	keyID := string(ciphertext[2 : 2+keyIDLen])
	if keyID != a.keyID {
		return nil, fmt.Errorf("crypto: unknown key id %q", keyID)
	}
	payload := ciphertext[2+keyIDLen:]

	gcm, err := a.gcm()
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("crypto: malformed envelope")
	}
	nonce, data := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}

func (a *AESGCM) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, fmt.Errorf("crypto: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: create gcm: %w", err)
	}
	return gcm, nil
}
