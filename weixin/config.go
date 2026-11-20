// Package weixin 提供微信公众号与微信小程序的鉴权、登录与消息校验能力。
//
// 支持多应用配置：公众号 OAuth2 登录、JS-SDK 签名、服务端消息签名与 AES 解密；
// 小程序 code2Session、手机号获取与加密数据解密。AccessToken/Ticket 自动缓存与刷新。
package weixin

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Kind 是微信应用类型。
type Kind string

const (
	// KindOfficial 表示公众号。
	KindOfficial Kind = "official"
	// KindMini 表示小程序。
	KindMini Kind = "mini"
)

// AppConfig 是单个微信应用配置。
type AppConfig struct {
	// Name 是应用标识，业务通过该名称获取客户端。
	Name string `yaml:"name"`
	// Type 为 official 或 mini。
	Type string `yaml:"type"`
	// AppID 与 AppSecret 是微信应用凭证。
	AppID     string `yaml:"app_id"`
	AppSecret string `yaml:"app_secret"`
	// Token 是服务端消息校验 Token。
	Token string `yaml:"token"`
	// EncodingAESKey 是消息加解密密钥（43 位）。
	EncodingAESKey string `yaml:"encoding_aes_key"`
	// OAuthRedirect 是公众号网页授权默认回调地址。
	OAuthRedirect string `yaml:"oauth_redirect"`
	// Default 标记默认应用。
	Default bool `yaml:"default"`
}

// Config 是微信组件配置。
type Config struct {
	Enabled bool          `yaml:"enabled"`
	Timeout time.Duration `yaml:"timeout"`
	Apps    []AppConfig   `yaml:"apps"`
}

// DefaultConfig 返回微信组件默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Timeout: 10 * time.Second,
	}
}

// NormalizeKind 归一化应用类型。
func NormalizeKind(value string) Kind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "official", "official_account", "mp", "gzh":
		return KindOfficial
	case "mini", "mini_program", "miniprogram", "xcx":
		return KindMini
	default:
		return Kind("")
	}
}

// Validate 校验应用配置。
func (a AppConfig) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("weixin: app name is required")
	}
	if NormalizeKind(a.Type) == "" {
		return fmt.Errorf("weixin: app %q has invalid type %q", a.Name, a.Type)
	}
	if strings.TrimSpace(a.AppID) == "" {
		return fmt.Errorf("weixin: app %q app_id is required", a.Name)
	}
	if strings.TrimSpace(a.AppSecret) == "" {
		return fmt.Errorf("weixin: app %q app_secret is required", a.Name)
	}
	if a.EncodingAESKey != "" && len(a.EncodingAESKey) != 43 {
		return fmt.Errorf("weixin: app %q encoding_aes_key must be 43 characters", a.Name)
	}
	return nil
}

var cfg = config.Register("weixin", DefaultConfig())

// GetConfig 返回当前微信配置快照。
func GetConfig() *Config { return cfg.Get() }
