// Package ctyun 封装天翼云（中国电信）云通信短信服务（EOP 签名 REST API）
// 与对象存储 ZOS（S3 兼容协议）能力。
//
// 配置来自 ctyun 配置段；导入本包并在 boot 启动后，
// 启用的子服务会注册为全局实例（GetSms/GetStorage），
// 短信启用时还会自动注册为 notify 的 sms 通道服务商。
//
// 短信使用天翼云 API 网关的 EOP AK/SK 签名认证，
// 对象存储 ZOS 通过 S3 兼容协议访问（endpoint 为 ZOS 接入域名）。
package ctyun

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// DefaultSMSEndpoint 是天翼云云通信短信服务的接入地址。
const DefaultSMSEndpoint = "https://sms-global.ctapi.ctyun.cn/sms/api/v1"

// Config 是天翼云配置。
type Config struct {
	// Enabled 控制是否启用天翼云集成。
	Enabled bool `yaml:"enabled"`
	// AccessKeyID 是访问密钥 ID（AK）。
	AccessKeyID string `yaml:"access_key_id"`
	// SecretAccessKey 是访问密钥 Secret（SK）。
	SecretAccessKey string `yaml:"secret_access_key"`
	// Timeout 是单次 API 调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// SMS 是云通信短信服务配置。
	SMS SMSConfig `yaml:"sms"`
	// Storage 是对象存储 ZOS 配置。
	Storage StorageConfig `yaml:"storage"`
}

// SMSConfig 是短信服务配置。
type SMSConfig struct {
	// Enabled 控制是否启用短信服务。
	Enabled bool `yaml:"enabled"`
	// SignName 是默认短信签名（已审核通过）。
	SignName string `yaml:"sign_name"`
	// Endpoint 覆盖默认的短信服务接入地址。
	Endpoint string `yaml:"endpoint"`
}

// StorageConfig 是对象存储 ZOS 配置。
type StorageConfig struct {
	// Enabled 控制是否启用 ZOS。
	Enabled bool `yaml:"enabled"`
	// Endpoint 是 ZOS 接入域名（host[:port]，不含 scheme），
	// 如 eos-region1.ctyun.cn。
	Endpoint string `yaml:"endpoint"`
	// Bucket 是默认存储桶名。
	Bucket string `yaml:"bucket"`
	// UseSSL 控制是否以 HTTPS 访问 ZOS，默认 true。
	UseSSL *bool `yaml:"use_ssl"`
}

// DefaultConfig 返回天翼云默认配置。
func DefaultConfig() Config {
	return Config{
		Timeout: 10 * time.Second,
		SMS:     SMSConfig{Endpoint: DefaultSMSEndpoint},
	}
}

var cfg = config.Register("ctyun", DefaultConfig())

// GetConfig 返回当前天翼云配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if c.Timeout <= 0 {
		c.Timeout = defaults.Timeout
	}
	if strings.TrimSpace(c.SMS.Endpoint) == "" {
		c.SMS.Endpoint = defaults.SMS.Endpoint
	}
	return c
}

// Validate 校验启用状态下的必填配置。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.SecretAccessKey) == "" {
		return fmt.Errorf("ctyun: access_key_id and secret_access_key are required when enabled")
	}
	if c.SMS.Enabled && strings.TrimSpace(c.SMS.SignName) == "" {
		return fmt.Errorf("ctyun: sms.sign_name is required when sms enabled")
	}
	if c.Storage.Enabled && (strings.TrimSpace(c.Storage.Endpoint) == "" || strings.TrimSpace(c.Storage.Bucket) == "") {
		return fmt.Errorf("ctyun: storage.endpoint and storage.bucket are required when storage enabled")
	}
	return nil
}

// useSSL 返回 ZOS 访问是否使用 HTTPS（默认 true）。
func (c Config) useSSL() bool {
	if c.Storage.UseSSL != nil {
		return *c.Storage.UseSSL
	}
	return true
}
