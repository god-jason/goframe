package weixin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Sign 计算微信服务端消息签名（sha1）。
func Sign(token, timestamp, nonce string) string {
	items := []string{token, timestamp, nonce}
	sort.Strings(items)
	sum := sha1.Sum([]byte(strings.Join(items, "")))
	return hex.EncodeToString(sum[:])
}

// SignMessage 计算加密消息签名，encrypt 为消息体密文。
func SignMessage(token, timestamp, nonce, encrypt string) string {
	items := []string{token, timestamp, nonce, encrypt}
	sort.Strings(items)
	sum := sha1.Sum([]byte(strings.Join(items, "")))
	return hex.EncodeToString(sum[:])
}

// VerifySignature 校验服务端消息签名。
func (c *Client) VerifySignature(signature, timestamp, nonce string) bool {
	return verifyHex(Sign(c.app.Token, timestamp, nonce), signature)
}

// VerifyMessageSignature 校验加密消息签名。
func (c *Client) VerifyMessageSignature(msgSignature, timestamp, nonce, encrypt string) bool {
	return verifyHex(SignMessage(c.app.Token, timestamp, nonce, encrypt), msgSignature)
}

// DecryptMessage 解密公众号/小程序推送的 AES 消息，返回明文字节。
func (c *Client) DecryptMessage(encrypted string) ([]byte, error) {
	if len(c.aesKey) != 32 {
		return nil, fmt.Errorf("weixin: app %q encoding_aes_key is not configured", c.app.Name)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("weixin: decode encrypted message: %w", err)
	}
	plaintext, err := decryptCBC(c.aesKey, c.aesKey[:aes.BlockSize], ciphertext, 32)
	if err != nil {
		return nil, err
	}
	if len(plaintext) < 20 {
		return nil, fmt.Errorf("weixin: decrypted message is too short")
	}
	messageLen := int(binary.BigEndian.Uint32(plaintext[16:20]))
	if messageLen < 0 || 20+messageLen > len(plaintext) {
		return nil, fmt.Errorf("weixin: invalid decrypted message length")
	}
	message := plaintext[20 : 20+messageLen]
	appID := strings.TrimSpace(string(plaintext[20+messageLen:]))
	if appID != c.app.AppID {
		return nil, fmt.Errorf("weixin: decrypted message appid mismatch")
	}
	return message, nil
}

// DecryptData 使用小程序 session_key 解密 encryptedData。
func DecryptData(sessionKey, encryptedData, iv string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("weixin: decode session_key: %w", err)
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, fmt.Errorf("weixin: invalid session_key length")
	}
	ivBytes, err := base64.StdEncoding.DecodeString(iv)
	if err != nil {
		return nil, fmt.Errorf("weixin: decode iv: %w", err)
	}
	if len(ivBytes) != aes.BlockSize {
		return nil, fmt.Errorf("weixin: invalid iv length")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, fmt.Errorf("weixin: decode encrypted data: %w", err)
	}
	return decryptCBC(key, ivBytes, ciphertext, aes.BlockSize)
}

// decryptCBC 执行 AES-CBC 解密并去除 PKCS7 填充。
func decryptCBC(key, iv, ciphertext []byte, blockSize int) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("weixin: create cipher: %w", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("weixin: invalid ciphertext length")
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, blockSize)
}

// pkcs7Unpad 去除 PKCS7 填充。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("weixin: invalid padding")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("weixin: invalid padding size")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("weixin: invalid padding bytes")
		}
	}
	return data[:len(data)-padding], nil
}

// randomNonce 生成随机字符串。
func randomNonce(length int) string {
	if length <= 0 {
		length = 16
	}
	buffer := make([]byte, (length+1)/2)
	if _, err := rand.Read(buffer); err != nil {
		return "goframe"
	}
	value := hex.EncodeToString(buffer)
	if len(value) > length {
		value = value[:length]
	}
	return value
}

// verifyHex 常量时间比较十六进制签名。
func verifyHex(expected, provided string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	provided = strings.ToLower(strings.TrimSpace(provided))
	if len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
