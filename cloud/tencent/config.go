// Package tencent 封装腾讯云官方 SDK，提供短信（SMS）、
// 语音通知（VMS）与对象存储（COS）能力。
//
// 配置来自 tencent 配置段；导入本包并在 boot 启动后，
// 启用的子服务会注册为全局实例（GetSms/GetVoice/GetStorage），
// 短信启用时还会自动注册为 notify 的 sms 通道服务商。
package tencent

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是腾讯云配置。
type Config struct {
	// Enabled 控制是否启用腾讯云集成。
	Enabled bool `yaml:"enabled"`
	// SecretID 是 API 密钥 SecretId。
	SecretID string `yaml:"secret_id"`
	// SecretKey 是 API 密钥 SecretKey。
	SecretKey string `yaml:"secret_key"`
	// Region 是地域，如 ap-guangzhou。
	Region string `yaml:"region"`
	// Timeout 是单次 API 调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// SMS 是短信服务配置。
	SMS SMSConfig `yaml:"sms"`
	// Voice 是语音通知服务配置。
	Voice VoiceConfig `yaml:"voice"`
	// Storage 是对象存储 COS 配置。
	Storage StorageConfig `yaml:"storage"`
}

// SMSConfig 是短信服务配置。
type SMSConfig struct {
	// Enabled 控制是否启用短信服务。
	Enabled bool `yaml:"enabled"`
	// SdkAppID 是短信应用的 SdkAppId（控制台创建）。
	SdkAppID string `yaml:"sdk_app_id"`
	// SignName 是短信签名。
	SignName string `yaml:"sign_name"`
}

// VoiceConfig 是语音通知服务配置。
type VoiceConfig struct {
	// Enabled 控制是否启用语音通知。
	Enabled bool `yaml:"enabled"`
	// SdkAppID 是语音应用的 SdkAppId。
	SdkAppID string `yaml:"sdk_app_id"`
	// PlayTimes 是语音播放次数，0 使用默认值 1。
	PlayTimes int `yaml:"play_times"`
}

// StorageConfig 是对象存储配置。
type StorageConfig struct {
	// Enabled 控制是否启用 COS。
	Enabled bool `yaml:"enabled"`
	// Bucket 是存储桶名，格式 name-appid。
	Bucket string `yaml:"bucket"`
	// Region 为空时复用顶层 region。
	Region string `yaml:"region"`
	// Endpoint 覆盖默认接入域名。
	Endpoint string `yaml:"endpoint"`
}

// DefaultConfig 返回腾讯云默认配置。
func DefaultConfig() Config {
	return Config{
		Region:  "ap-guangzhou",
		Timeout: 10 * time.Second,
	}
}

var cfg = config.Register("tencent", DefaultConfig())

// GetConfig 返回当前腾讯云配置快照。
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
	if c.Voice.PlayTimes < 0 {
		c.Voice.PlayTimes = 0
	}
	return c
}

// Validate 校验启用状态下的必填配置。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.SecretID) == "" || strings.TrimSpace(c.SecretKey) == "" {
		return fmt.Errorf("tencent: secret_id and secret_key are required when enabled")
	}
	if c.SMS.Enabled && strings.TrimSpace(c.SMS.SdkAppID) == "" {
		return fmt.Errorf("tencent: sms.sdk_app_id is required when sms enabled")
	}
	if c.Voice.Enabled && strings.TrimSpace(c.Voice.SdkAppID) == "" {
		return fmt.Errorf("tencent: voice.sdk_app_id is required when voice enabled")
	}
	if c.Storage.Enabled && strings.TrimSpace(c.Storage.Bucket) == "" {
		return fmt.Errorf("tencent: storage.bucket is required when storage enabled")
	}
	return nil
}

// bucketURL 返回 COS 存储桶接入域名。
func (c Config) bucketURL() string {
	if ep := strings.TrimSpace(c.Storage.Endpoint); ep != "" {
		return ep
	}
	region := strings.TrimSpace(c.Storage.Region)
	if region == "" {
		region = c.Region
	}
	return fmt.Sprintf("https://%s.cos.%s.myqcloud.com", c.Storage.Bucket, region)
}
