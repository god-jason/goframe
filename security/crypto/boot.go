package crypto

import (
	"context"
	"fmt"
	"strings"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/config"
)

var cfg = config.Register("security.encryption", DefaultConfig())

// Current 返回当前加密配置快照。
func Current() *Config { return cfg.Get() }

func init() {
	_ = boot.Register("crypto", &boot.Task{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config"},
	})
}

// Startup 根据配置初始化加密器。未提供密钥时保持未启用状态。
func Startup(context.Context) error {
	current := Current()
	if current.Algorithm != "" && !strings.EqualFold(current.Algorithm, "aes-256-gcm") {
		return fmt.Errorf("crypto: unsupported algorithm %q", current.Algorithm)
	}
	if current.Key == "" {
		return nil
	}

	key, err := NewKeyFromBase64(current.Key)
	if err != nil {
		// 兼容直接提供 32 字节原始字符串的配置方式。
		if len(current.Key) != 32 {
			return err
		}
		key = []byte(current.Key)
	}
	encryptor, err := NewAESGCM(current.KeyID, key)
	if err != nil {
		return err
	}
	Set(encryptor)
	return nil
}

// Shutdown 释放加密器。
func Shutdown(context.Context) error {
	Set(nil)
	return nil
}
