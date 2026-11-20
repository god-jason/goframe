// Package aliyun 封装阿里云官方 SDK，提供短信（dysmsapi）、
// 语音通知（dyvmsapi）与对象存储（OSS）能力。
//
// 配置来自 aliyun 配置段；导入本包并在 boot 启动后，
// 启用的子服务会注册为全局实例（GetSms/GetVoice/GetStorage），
// 短信启用时还会自动注册为 notify 的 sms 通道服务商。
package aliyun

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是阿里云配置。
type Config struct {
	// Enabled 控制是否启用阿里云集成。
	Enabled bool `yaml:"enabled"`
	// AccessKeyID 是访问密钥 ID。
	AccessKeyID string `yaml:"access_key_id"`
	// AccessKeySecret 是访问密钥 Secret。
	AccessKeySecret string `yaml:"access_key_secret"`
	// Region 是地域 ID，如 cn-hangzhou。
	Region string `yaml:"region"`
	// Timeout 是单次 API 调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// SMS 是短信服务配置。
	SMS SMSConfig `yaml:"sms"`
	// Voice 是语音通知服务配置。
	Voice VoiceConfig `yaml:"voice"`
	// Storage 是对象存储 OSS 配置。
	Storage StorageConfig `yaml:"storage"`
}

// SMSConfig 是短信服务配置。
type SMSConfig struct {
	// Enabled 控制是否启用短信服务。
	Enabled bool `yaml:"enabled"`
	// SignName 是短信签名。
	SignName string `yaml:"sign_name"`
	// Endpoint 覆盖默认的 dysmsapi 接入地址。
	Endpoint string `yaml:"endpoint"`
}

// VoiceConfig 是语音通知服务配置。
type VoiceConfig struct {
	// Enabled 控制是否启用语音通知。
	Enabled bool `yaml:"enabled"`
	// CalledShowNumber 是被叫显号（已在控制台报备的主叫号码）。
	CalledShowNumber string `yaml:"called_show_number"`
	// Endpoint 覆盖默认的 dyvmsapi 接入地址。
	Endpoint string `yaml:"endpoint"`
}

// StorageConfig 是对象存储配置。
type StorageConfig struct {
	// Enabled 控制是否启用 OSS。
	Enabled bool `yaml:"enabled"`
	// Endpoint 是 OSS 接入域名，如 https://oss-cn-hangzhou.aliyuncs.com。
	Endpoint string `yaml:"endpoint"`
	// Bucket 是默认存储桶名。
	Bucket string `yaml:"bucket"`
}

// DefaultConfig 返回阿里云默认配置。
func DefaultConfig() Config {
	return Config{
		Region:  "cn-hangzhou",
		Timeout: 10 * time.Second,
	}
}

var cfg = config.Register("aliyun", DefaultConfig())

// GetConfig 返回当前阿里云配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(c.Region) == "" {
		c.Region = defaults.Region
	}
	if c.Timeout <= 0 {
		c.Timeout = defaults.Timeout
	}
	return c
}

// Validate 校验启用状态下的必填配置。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.AccessKeySecret) == "" {
		return fmt.Errorf("aliyun: access_key_id and access_key_secret are required when enabled")
	}
	if c.SMS.Enabled && strings.TrimSpace(c.SMS.SignName) == "" {
		return fmt.Errorf("aliyun: sms.sign_name is required when sms enabled")
	}
	if c.Voice.Enabled && strings.TrimSpace(c.Voice.CalledShowNumber) == "" {
		return fmt.Errorf("aliyun: voice.called_show_number is required when voice enabled")
	}
	if c.Storage.Enabled && (strings.TrimSpace(c.Storage.Endpoint) == "" || strings.TrimSpace(c.Storage.Bucket) == "") {
		return fmt.Errorf("aliyun: storage.endpoint and storage.bucket are required when storage enabled")
	}
	return nil
}

// smsEndpoint 返回短信服务接入地址。
func (c Config) smsEndpoint() string {
	if ep := strings.TrimSpace(c.SMS.Endpoint); ep != "" {
		return ep
	}
	return fmt.Sprintf("dysmsapi.%s.aliyuncs.com", c.Region)
}

// voiceEndpoint 返回语音服务接入地址。
func (c Config) voiceEndpoint() string {
	if ep := strings.TrimSpace(c.Voice.Endpoint); ep != "" {
		return ep
	}
	return fmt.Sprintf("dyvmsapi.%s.aliyuncs.com", c.Region)
}
