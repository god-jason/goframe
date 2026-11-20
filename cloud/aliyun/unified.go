package aliyun

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/cloud"
)

// 确认阿里云客户端实现统一云接口。
var (
	_ cloud.Sms     = (*Sms)(nil)
	_ cloud.Voice   = (*Voice)(nil)
	_ cloud.Storage = (*Storage)(nil)
)

// SendSms 实现 cloud.Sms 统一接口；
// req.Sign 为空时使用配置中的短信签名。
func (s *Sms) SendSms(ctx context.Context, req *cloud.SmsRequest) error {
	if req == nil {
		return fmt.Errorf("aliyun: sms request is nil")
	}
	sign := req.Sign
	if sign == "" {
		sign = s.cfg.SMS.SignName
	}
	return s.Send(ctx, req.PhoneNumbers, sign, req.TemplateID, req.Params)
}

// PlayVoice 实现 cloud.Voice 统一接口，使用 TTS 模板语音单呼；
// 阿里云语音必须使用审核通过的 TTS 模板 ID（req.TemplateID）。
func (v *Voice) PlayVoice(ctx context.Context, req *cloud.VoiceRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("aliyun: voice request is nil")
	}
	if req.TemplateID == "" {
		return "", fmt.Errorf("aliyun: voice template_id (TTS 模板) is required")
	}
	return v.SingleCallByTts(ctx, req.Callee, req.TemplateID, req.Params)
}
