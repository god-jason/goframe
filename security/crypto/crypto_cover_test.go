package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestHMACSHA256 覆盖包内 HMAC-SHA256 辅助函数：与标准库计算结果一致。
func TestHMACSHA256(t *testing.T) {
	sum := HMACSHA256([]byte("secret-key"), []byte("message"))
	expect := hmac.New(sha256.New, []byte("secret-key"))
	expect.Write([]byte("message"))
	if got, want := hex.EncodeToString(sum), hex.EncodeToString(expect.Sum(nil)); got != want {
		t.Fatalf("HMACSHA256 = %s, want %s", got, want)
	}
	// 空密钥与空消息同样应返回固定长度摘要。
	if len(HMACSHA256(nil, nil)) != sha256.Size {
		t.Fatalf("digest len = %d, want %d", len(HMACSHA256(nil, nil)), sha256.Size)
	}
}

// 说明：crypto.go 中 cipher.NewGCM 的错误分支（"create gcm"）为逻辑不可达的防御分支——
// aes.NewCipher 仅接受 16/24/32 字节密钥，成功返回的 AES 分组块大小恒为 16 字节，
// cipher.NewGCM 对合法 AES 分组永不返回错误，故该分支无法在测试中触发。
