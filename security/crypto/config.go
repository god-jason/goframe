package crypto

import "sync"

// Config 是数据加密配置。
type Config struct {
	Algorithm   string `mapstructure:"algorithm"`
	KeyProvider string `mapstructure:"key_provider"`
	KeyID       string `mapstructure:"key_id"`
	Key         string `mapstructure:"key"`
}

// DefaultConfig 返回加密默认配置。
func DefaultConfig() Config {
	return Config{
		Algorithm:   "aes-256-gcm",
		KeyProvider: "env",
		KeyID:       "primary",
	}
}

var global = struct {
	sync.RWMutex
	encryptor Encryptor
}{}

// Get 返回全局加密器；未配置密钥时返回 nil。
func Get() Encryptor {
	global.RLock()
	defer global.RUnlock()
	return global.encryptor
}

// Set 替换全局加密器。
func Set(encryptor Encryptor) {
	global.Lock()
	global.encryptor = encryptor
	global.Unlock()
}
