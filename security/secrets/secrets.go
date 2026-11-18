// Package secrets 提供密钥/敏感配置的读取抽象。
package secrets

import (
	"context"
	"fmt"
	"os"
)

// Provider 是密钥提供者。生产环境建议接入 Vault、KMS 或 Secret Manager。
type Provider interface {
	Get(ctx context.Context, name string) (string, error)
}

// EnvProvider 从环境变量读取密钥，可设置统一前缀。
type EnvProvider struct {
	prefix string
}

// NewEnv 创建环境变量密钥提供者。
func NewEnv(prefix string) *EnvProvider {
	return &EnvProvider{prefix: prefix}
}

// New 根据配置创建密钥提供者。
// vault 模式从 VAULT_ADDR、VAULT_TOKEN、VAULT_NAMESPACE、VAULT_MOUNT 读取连接信息。
func New(provider string) (Provider, error) {
	switch provider {
	case "", "env":
		return NewEnv(""), nil
	case "vault":
		cfg := DefaultVaultConfig()
		cfg.Address = os.Getenv("VAULT_ADDR")
		cfg.Token = os.Getenv("VAULT_TOKEN")
		cfg.Namespace = os.Getenv("VAULT_NAMESPACE")
		if mount := os.Getenv("VAULT_MOUNT"); mount != "" {
			cfg.Mount = mount
		}
		return NewVault(cfg)
	default:
		return nil, fmt.Errorf("secrets: unsupported provider %q", provider)
	}
}

// Get 读取环境变量。
func (p *EnvProvider) Get(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, ok := os.LookupEnv(p.prefix + name)
	if !ok || value == "" {
		return "", fmt.Errorf("secrets: %s is not configured", name)
	}
	return value, nil
}
