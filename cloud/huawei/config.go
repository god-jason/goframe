// Package huawei 封装华为云官方 SDK，提供短信（SMS）、
// 语音通话（VCP）与对象存储（OBS）能力。
//
// 配置来自 huawei 配置段；导入本包并在 boot 启动后，
// 启用的子服务会注册为全局实例（GetSms/GetVoice/GetStorage），
// 短信启用时还会自动注册为 notify 的 sms 通道服务商。
//
// 注意：华为云短信与 VCP 语音均需开通对应服务并配置 ProjectID；
// 语音通话基于 VCP 1.0 executeCall 接口实现。
package huawei

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是华为云配置。
type Config struct {
	// Enabled 控制是否启用华为云集成。
	Enabled bool `yaml:"enabled"`
	// AccessKeyID 是 AK。
	AccessKeyID string `yaml:"access_key_id"`
	// SecretAccessKey 是 SK。
	SecretAccessKey string `yaml:"secret_access_key"`
	// ProjectID 是区域级项目 ID（短信与 VCP 必填）。
	ProjectID string `yaml:"project_id"`
	// Region 是区域，如 cn-north-4。
	Region string `yaml:"region"`
	// Timeout 是单次 API 调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// SMS 是短信服务配置。
	SMS SMSConfig `yaml:"sms"`
	// Voice 是语音通话（VCP）配置。
	Voice VoiceConfig `yaml:"voice"`
	// Storage 是对象存储 OBS 配置。
	Storage StorageConfig `yaml:"storage"`
}

// SMSConfig 是短信服务配置。
type SMSConfig struct {
	// Enabled 控制是否启用短信服务。
	Enabled bool `yaml:"enabled"`
	// Signature 是短信签名。
	Signature string `yaml:"signature"`
	// From 是显示源号码（发送方号码，控制台申请）。
	From string `yaml:"from"`
}

// VoiceConfig 是语音通话配置。
type VoiceConfig struct {
	// Enabled 控制是否启用语音通话。
	Enabled bool `yaml:"enabled"`
	// ShowNumber 是主叫显示号码（已购买的虚拟号码）。
	ShowNumber string `yaml:"show_number"`
	// PlayTimes 是语音播放次数，0 使用默认值 1。
	PlayTimes int `yaml:"play_times"`
	// CountryCode 是被叫号码国家码，如 CN。
	CountryCode string `yaml:"country_code"`
	// Endpoint 覆盖默认的 VCP 接入地址。
	Endpoint string `yaml:"endpoint"`
}

// StorageConfig 是对象存储配置。
type StorageConfig struct {
	// Enabled 控制是否启用 OBS。
	Enabled bool `yaml:"enabled"`
	// Endpoint 是 OBS 接入域名，如 https://obs.cn-north-4.myhuaweicloud.com。
	Endpoint string `yaml:"endpoint"`
	// Bucket 是默认桶名。
	Bucket string `yaml:"bucket"`
}

// DefaultConfig 返回华为云默认配置。
func DefaultConfig() Config {
	return Config{
		Region:  "cn-north-4",
		Timeout: 10 * time.Second,
		Voice:   VoiceConfig{CountryCode: "CN"},
	}
}

var cfg = config.Register("huawei", DefaultConfig())

// GetConfig 返回当前华为云配置快照。
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
	if strings.TrimSpace(c.Voice.CountryCode) == "" {
		c.Voice.CountryCode = defaults.Voice.CountryCode
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
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.SecretAccessKey) == "" {
		return fmt.Errorf("huawei: access_key_id and secret_access_key are required when enabled")
	}
	if c.SMS.Enabled && (strings.TrimSpace(c.ProjectID) == "" || strings.TrimSpace(c.SMS.Signature) == "" || strings.TrimSpace(c.SMS.From) == "") {
		return fmt.Errorf("huawei: project_id, sms.signature and sms.from are required when sms enabled")
	}
	if c.Voice.Enabled && (strings.TrimSpace(c.ProjectID) == "" || strings.TrimSpace(c.Voice.ShowNumber) == "") {
		return fmt.Errorf("huawei: project_id and voice.show_number are required when voice enabled")
	}
	if c.Storage.Enabled && (strings.TrimSpace(c.Storage.Endpoint) == "" || strings.TrimSpace(c.Storage.Bucket) == "") {
		return fmt.Errorf("huawei: storage.endpoint and storage.bucket are required when storage enabled")
	}
	return nil
}

// vcpEndpoint 返回 VCP 语音通话接入地址。
func (c Config) vcpEndpoint() string {
	if ep := strings.TrimSpace(c.Voice.Endpoint); ep != "" {
		return ep
	}
	return fmt.Sprintf("https://vcp.%s.myhuaweicloud.com", c.Region)
}
