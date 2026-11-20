package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v3/client"
	"github.com/alibabacloud-go/tea/tea"
)

// Sms 是阿里云短信服务客户端。
type Sms struct {
	client *dysmsapi.Client
	cfg    Config
}

// NewSms 按配置创建短信客户端。
func NewSms(cfg Config) (*Sms, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	timeoutMs := int(cfg.Timeout / 1000)
	client, err := dysmsapi.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(cfg.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AccessKeySecret),
		Endpoint:        tea.String(cfg.smsEndpoint()),
		ReadTimeout:     tea.Int(timeoutMs),
		ConnectTimeout:  tea.Int(timeoutMs),
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun: create sms client: %w", err)
	}
	return &Sms{client: client, cfg: cfg}, nil
}

// Send 使用签名与模板向号码列表发送短信；params 为模板变量（JSON 对象）。
func (s *Sms) Send(ctx context.Context, phoneNumbers []string, signName, templateCode string, params map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(phoneNumbers) == 0 {
		return fmt.Errorf("aliyun: sms requires at least one phone number")
	}
	if strings.TrimSpace(templateCode) == "" {
		return fmt.Errorf("aliyun: sms template_code is required")
	}
	req := &dysmsapi.SendSmsRequest{
		PhoneNumbers: tea.String(strings.Join(phoneNumbers, ",")),
		SignName:     tea.String(signName),
		TemplateCode: tea.String(templateCode),
	}
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("aliyun: encode sms template param: %w", err)
		}
		req.TemplateParam = tea.String(string(raw))
	}
	resp, err := s.client.SendSms(req)
	if err != nil {
		return fmt.Errorf("aliyun: send sms: %w", err)
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "OK" {
		return fmt.Errorf("aliyun: send sms: code %s: %s", code, tea.StringValue(resp.Body.Message))
	}
	return nil
}

// SmsProvider 把阿里云短信适配为 notify 的短信服务商。
//
// notify 消息的 Metadata 约定：
//   - template_code：短信模板 ID（必填）；
//   - 其余键值对作为模板变量传入 TemplateParam。
type SmsProvider struct {
	sms *Sms
}

// NewSmsProvider 创建 notify 短信服务商适配器。
func NewSmsProvider(cfg Config) (*SmsProvider, error) {
	sms, err := NewSms(cfg)
	if err != nil {
		return nil, err
	}
	return &SmsProvider{sms: sms}, nil
}

// Send 实现 notify.Provider：to 为号码列表，params 携带 template_code 与模板变量。
func (p *SmsProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	vars := make(map[string]string, len(params))
	templateCode := ""
	for k, v := range params {
		if k == "template_code" {
			templateCode = v
			continue
		}
		vars[k] = v
	}
	if templateCode == "" {
		return fmt.Errorf("aliyun: sms metadata requires template_code")
	}
	return p.sms.Send(ctx, to, p.sms.cfg.SMS.SignName, templateCode, vars)
}
