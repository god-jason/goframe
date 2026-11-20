// Package payment 提供在线支付统一网关封装，支持微信支付（APIv3）与支付宝，
// 覆盖 Native 扫码、JSAPI/小程序、H5/APP、网页支付、订单查询、关单、退款、
// 对账账单下载与异步回调通知。
//
// 组件只做协议网关，不持久化订单；订单状态落库由业务方在回调 handler 中完成。
// 回调自动完成验签、幂等去重（复用 lock/cache），再分发给 OnNotify 注册的业务 handler。
//
// 金额统一使用分（int64）；适配器内部负责微信（分）与支付宝（元字符串）的换算。
package payment

import (
	"fmt"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// Config 是在线支付配置。
type Config struct {
	// Enabled 控制是否启用支付组件。
	Enabled bool `yaml:"enabled"`
	// NotifyPath 是回调路由前缀，登记为 POST {path}/wechat 与 {path}/alipay。
	NotifyPath string `yaml:"notify_path"`
	// Timeout 是单次网关调用超时。
	Timeout time.Duration `yaml:"timeout"`
	// Idempotency 是回调幂等参数。
	Idempotency IdempotencyConfig `yaml:"idempotency"`
	// Wechat 是微信支付渠道配置。
	Wechat WechatConfig `yaml:"wechat"`
	// Alipay 是支付宝渠道配置。
	Alipay AlipayConfig `yaml:"alipay"`
}

// IdempotencyConfig 是回调幂等配置。
type IdempotencyConfig struct {
	// DoneTTL 是已处理回调标记的保留时长。
	DoneTTL time.Duration `yaml:"done_ttl"`
	// ProcessingTTL 是处理中互斥锁的存活时间。
	ProcessingTTL time.Duration `yaml:"processing_ttl"`
}

// WechatConfig 是微信支付（APIv3）渠道配置。
type WechatConfig struct {
	// Enabled 控制是否启用微信渠道。
	Enabled bool `yaml:"enabled"`
	// AppID 是微信应用 AppID。
	AppID string `yaml:"app_id"`
	// MchID 是商户号。
	MchID string `yaml:"mch_id"`
	// SerialNo 是商户 API 证书序列号。
	SerialNo string `yaml:"serial_no"`
	// PrivateKey 是商户 API 私钥，PEM 内容或文件路径。
	PrivateKey string `yaml:"private_key"`
	// APIv3Key 是商户 APIv3 密钥，用于回调解密与平台证书验签。
	APIv3Key string `yaml:"apiv3_key"`
	// NotifyURL 是默认回调地址，下单时可逐单覆盖。
	NotifyURL string `yaml:"notify_url"`
}

// AlipayConfig 是支付宝渠道配置。
type AlipayConfig struct {
	// Enabled 控制是否启用支付宝渠道。
	Enabled bool `yaml:"enabled"`
	// AppID 是支付宝应用 AppID。
	AppID string `yaml:"app_id"`
	// PrivateKey 是应用私钥，PEM 内容或文件路径。
	PrivateKey string `yaml:"private_key"`
	// PublicKey 是支付宝公钥（普通模式），PEM 内容或文件路径。
	PublicKey string `yaml:"public_key"`
	// AppCertFile 是应用公钥证书路径；三个证书文件任一非空即启用证书模式。
	AppCertFile string `yaml:"app_cert_file"`
	// AlipayCertFile 是支付宝公钥证书路径。
	AlipayCertFile string `yaml:"alipay_cert_file"`
	// RootCertFile 是支付宝根证书路径。
	RootCertFile string `yaml:"root_cert_file"`
	// Production 为 true 时使用正式环境，否则沙箱。
	Production bool `yaml:"production"`
	// NotifyURL 是默认异步通知地址，下单时可逐单覆盖。
	NotifyURL string `yaml:"notify_url"`
	// ReturnURL 是默认同步跳转地址（page/h5 场景）。
	ReturnURL string `yaml:"return_url"`
}

// DefaultConfig 返回在线支付默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled:    false,
		NotifyPath: "/payment/notify",
		Timeout:    10 * time.Second,
		Idempotency: IdempotencyConfig{
			DoneTTL:       48 * time.Hour,
			ProcessingTTL: 30 * time.Second,
		},
	}
}

var cfg = config.Register("payment", DefaultConfig())

// GetConfig 返回当前支付配置快照。
func GetConfig() *Config { return cfg.Get() }

// normalize 补全非法配置，保证组件可安全启动。
func (c Config) normalize() Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(c.NotifyPath) == "" {
		c.NotifyPath = defaults.NotifyPath
	}
	if c.Timeout <= 0 {
		c.Timeout = defaults.Timeout
	}
	if c.Idempotency.DoneTTL <= 0 {
		c.Idempotency.DoneTTL = defaults.Idempotency.DoneTTL
	}
	if c.Idempotency.ProcessingTTL <= 0 {
		c.Idempotency.ProcessingTTL = defaults.Idempotency.ProcessingTTL
	}
	if c.Idempotency.ProcessingTTL >= c.Idempotency.DoneTTL {
		c.Idempotency.ProcessingTTL = time.Second
	}
	return c
}

// Validate 校验启用状态下的必填凭据：至少一个渠道启用且凭据齐全。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !c.Wechat.Enabled && !c.Alipay.Enabled {
		return fmt.Errorf("payment: at least one channel (wechat/alipay) must be enabled")
	}
	if c.Wechat.Enabled {
		if err := c.Wechat.validate(); err != nil {
			return err
		}
	}
	if c.Alipay.Enabled {
		if err := c.Alipay.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validate 校验微信渠道凭据。
func (w WechatConfig) validate() error {
	missing := func(field string) error {
		return fmt.Errorf("payment: wechat.%s is required when wechat is enabled", field)
	}
	if strings.TrimSpace(w.AppID) == "" {
		return missing("app_id")
	}
	if strings.TrimSpace(w.MchID) == "" {
		return missing("mch_id")
	}
	if strings.TrimSpace(w.SerialNo) == "" {
		return missing("serial_no")
	}
	if strings.TrimSpace(w.PrivateKey) == "" {
		return missing("private_key")
	}
	if strings.TrimSpace(w.APIv3Key) == "" {
		return missing("apiv3_key")
	}
	return nil
}

// certMode 报告支付宝是否使用证书模式。
func (a AlipayConfig) certMode() bool {
	return strings.TrimSpace(a.AppCertFile) != "" ||
		strings.TrimSpace(a.AlipayCertFile) != "" ||
		strings.TrimSpace(a.RootCertFile) != ""
}

// validate 校验支付宝渠道凭据。
func (a AlipayConfig) validate() error {
	missing := func(field string) error {
		return fmt.Errorf("payment: alipay.%s is required when alipay is enabled", field)
	}
	if strings.TrimSpace(a.AppID) == "" {
		return missing("app_id")
	}
	if strings.TrimSpace(a.PrivateKey) == "" {
		return missing("private_key")
	}
	if a.certMode() {
		if strings.TrimSpace(a.AppCertFile) == "" {
			return missing("app_cert_file")
		}
		if strings.TrimSpace(a.AlipayCertFile) == "" {
			return missing("alipay_cert_file")
		}
		if strings.TrimSpace(a.RootCertFile) == "" {
			return missing("root_cert_file")
		}
		return nil
	}
	if strings.TrimSpace(a.PublicKey) == "" {
		return missing("public_key")
	}
	return nil
}
