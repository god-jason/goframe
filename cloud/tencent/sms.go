package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	smsapi "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20190711"
)

// Sms 是腾讯云短信服务客户端。
type Sms struct {
	client *smsapi.Client
	cfg    Config
}

// NewSms 按配置创建短信客户端。
func NewSms(cfg Config) (*Sms, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	credential := common.NewCredential(cfg.SecretID, cfg.SecretKey)
	client, err := smsapi.NewClient(credential, cfg.Region, profile.NewClientProfile())
	if err != nil {
		return nil, fmt.Errorf("tencent: create sms client: %w", err)
	}
	return &Sms{client: client, cfg: cfg}, nil
}

// Send 使用签名与模板向号码列表发送短信；templateParams 为模板变量（按顺序）。
// 返回腾讯云请求 ID。
func (s *Sms) Send(ctx context.Context, phoneNumbers []string, signName, templateID string, templateParams []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(phoneNumbers) == 0 {
		return "", fmt.Errorf("tencent: sms requires at least one phone number")
	}
	if strings.TrimSpace(templateID) == "" {
		return "", fmt.Errorf("tencent: sms template_id is required")
	}
	req := &smsapi.SendSmsRequest{
		PhoneNumberSet:   common.StringPtrs(phoneNumbers),
		SmsSdkAppid:      common.StringPtr(s.cfg.SMS.SdkAppID),
		Sign:             common.StringPtr(signName),
		TemplateID:       common.StringPtr(templateID),
		TemplateParamSet: common.StringPtrs(templateParams),
	}
	resp, err := s.client.SendSmsWithContext(ctx, req)
	if err != nil {
		return "", fmt.Errorf("tencent: send sms: %w", err)
	}
	requestID := ""
	if resp.Response != nil {
		requestID = deref(resp.Response.RequestId)
		for _, status := range resp.Response.SendStatusSet {
			if code := deref(status.Code); code != "Ok" {
				return requestID, fmt.Errorf("tencent: send sms: code %s: %s", code, deref(status.Message))
			}
		}
	}
	return requestID, nil
}

// SmsProvider 把腾讯云短信适配为 notify 的短信服务商。
//
// notify 消息的 Metadata 约定：
//   - template_id：短信模板 ID（必填）；
//   - template_params：模板变量，英文逗号分隔，按模板占位顺序传入。
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

// Send 实现 notify.Provider：to 为号码列表，params 携带 template_id 与 template_params。
func (p *SmsProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	templateID := params["template_id"]
	if templateID == "" {
		return fmt.Errorf("tencent: sms metadata requires template_id")
	}
	var templateParams []string
	if raw := strings.TrimSpace(params["template_params"]); raw != "" {
		templateParams = strings.Split(raw, ",")
	}
	_, err := p.sms.Send(ctx, to, p.sms.cfg.SMS.SignName, templateID, templateParams)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
