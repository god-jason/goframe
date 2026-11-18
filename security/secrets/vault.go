package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VaultConfig 是 HashiCorp Vault KV v2 配置。
type VaultConfig struct {
	Address   string        `mapstructure:"address"`
	Token     string        `mapstructure:"token"`
	Namespace string        `mapstructure:"namespace"`
	Mount     string        `mapstructure:"mount"`
	Timeout   time.Duration `mapstructure:"timeout"`
}

// DefaultVaultConfig 返回 Vault 默认配置。
func DefaultVaultConfig() VaultConfig {
	return VaultConfig{
		Mount:   "secret",
		Timeout: 5 * time.Second,
	}
}

// VaultProvider 从 Vault KV v2 读取密钥。
type VaultProvider struct {
	cfg    VaultConfig
	client *http.Client
}

// NewVault 创建 Vault 密钥提供者。
func NewVault(cfg VaultConfig) (*VaultProvider, error) {
	cfg.Address = strings.TrimRight(strings.TrimSpace(cfg.Address), "/")
	if cfg.Address == "" {
		return nil, fmt.Errorf("secrets: vault address is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("secrets: vault token is required")
	}
	if cfg.Mount == "" {
		cfg.Mount = "secret"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &VaultProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// Get 读取密钥。优先返回 value 字段，其次在只有一个字段时返回该字段。
func (p *VaultProvider) Get(ctx context.Context, name string) (string, error) {
	data, err := p.read(ctx, name)
	if err != nil {
		return "", err
	}
	if value, ok := data["value"]; ok {
		return value, nil
	}
	if len(data) == 1 {
		for _, value := range data {
			return value, nil
		}
	}
	return "", fmt.Errorf("secrets: vault secret %q has no value field", name)
}

// Put 写入密钥字段。
func (p *VaultProvider) Put(ctx context.Context, name string, values map[string]string) error {
	payload, err := json.Marshal(map[string]any{"data": values})
	if err != nil {
		return fmt.Errorf("secrets: encode vault payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.dataURL(name), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("secrets: create vault request: %w", err)
	}
	p.setHeaders(request)
	request.Header.Set("Content-Type", "application/json")

	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("secrets: vault write: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("secrets: vault write status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *VaultProvider) read(ctx context.Context, name string) (map[string]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.dataURL(name), nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: create vault request: %w", err)
	}
	p.setHeaders(request)

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("secrets: vault read: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("secrets: vault read status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("secrets: decode vault response: %w", err)
	}
	if payload.Data.Data == nil {
		return nil, fmt.Errorf("secrets: vault secret %q not found", name)
	}
	return payload.Data.Data, nil
}

func (p *VaultProvider) dataURL(name string) string {
	return fmt.Sprintf("%s/v1/%s/data/%s", p.cfg.Address, strings.Trim(p.cfg.Mount, "/"), strings.TrimLeft(name, "/"))
}

func (p *VaultProvider) setHeaders(request *http.Request) {
	request.Header.Set("X-Vault-Token", p.cfg.Token)
	if p.cfg.Namespace != "" {
		request.Header.Set("X-Vault-Namespace", p.cfg.Namespace)
	}
}
