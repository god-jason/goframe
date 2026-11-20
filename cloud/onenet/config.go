// Package onenet 封装中国移动 OneNET 物联网开放平台的短信能力平台（REST API），
// 提供模板签名短信下发能力。
//
// 配置来自 onenet 配置段；导入本包并在 boot 启动后，
// 启用的短信服务会注册为全局实例（GetSms），
// 并自动注册为 notify 的 sms 通道服务商。
//
// OneNET 短信通过 SIcode 鉴权并依赖平台侧的签名/模板审核，
// 建议在账户设置中把服务器出口 IP 加入白名单。
package onenet

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// DefaultEndpoint 是 OneNET 短信能力平台的接入地址。
const DefaultEndpoint = "http://api.sms.heclouds.com"

// Config 是 OneNET 配置。
type Config struct {
	// Enabled 控制是否启用 OneNET 集成。
	Enabled bool `yaml:"enabled"`
	// SIcode 是短信能力平台的 SI 唯一标识（开通短信能力后获得）。
	SIcode string `yaml:"sicode"`
	// Endpoint 覆盖默认接入地址。
	Endpoint string `yaml:"endpoint"`
	// Timeout 是单次 API 调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// SMS 是短信服务配置。
	SMS SMSConfig `yaml:"sms"`
}

// SMSConfig 是短信服务配置。
type SMSConfig struct {
	// Enabled 控制是否启用短信服务。
	Enabled bool `yaml:"enabled"`
	// SignID 是默认签名 ID（创建签名并审核通过后获得）。
	SignID string `yaml:"sign_id"`
}

// DefaultConfig 返回 OneNET 默认配置。
func DefaultConfig() Config {
	return Config{
		Endpoint: DefaultEndpoint,
		Timeout:  10 * time.Second,
	}
}

var cfg = config.Register("onenet", DefaultConfig())

// GetConfig 返回当前 OneNET 配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(c.Endpoint) == "" {
		c.Endpoint = defaults.Endpoint
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
	if strings.TrimSpace(c.SIcode) == "" {
		return fmt.Errorf("onenet: sicode is required when enabled")
	}
	if c.SMS.Enabled && strings.TrimSpace(c.SMS.SignID) == "" {
		return fmt.Errorf("onenet: sms.sign_id is required when sms enabled")
	}
	return nil
}
