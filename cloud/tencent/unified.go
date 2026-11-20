package tencent

import (
	"context"
	"fmt"

	"github.com/god-jason/goframe/cloud"
)

// 确认腾讯云客户端实现统一云接口。
var (
	_ cloud.Sms     = (*Sms)(nil)
	_ cloud.Voice   = (*Voice)(nil)
	_ cloud.Storage = (*Storage)(nil)
)

// SendSms 实现 cloud.Sms 统一接口；
// req.Sign 为空时使用配置中的短信签名，
// 模板变量取 req.TemplateParams，为空时按序展开 req.Params。
func (s *Sms) SendSms(ctx context.Context, req *cloud.SmsRequest) error {
	if req == nil {
		return fmt.Errorf("tencent: sms request is nil")
	}
	sign := req.Sign
	if sign == "" {
		sign = s.cfg.SMS.SignName
	}
	_, err := s.Send(ctx, req.PhoneNumbers, sign, req.TemplateID, cloud.SmsTemplateParams(req))
	return err
}

// PlayVoice 实现 cloud.Voice 统一接口，使用 TTS 模板语音播报；
// req.TemplateID 必填，req.PlayTimes 为 0 时使用配置默认值。
func (v *Voice) PlayVoice(ctx context.Context, req *cloud.VoiceRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("tencent: voice request is nil")
	}
	if req.TemplateID == "" {
		return "", fmt.Errorf("tencent: voice template_id (TTS 模板) is required")
	}
	return v.SendTtsVoice(ctx, req.Callee, req.TemplateID, req.PlayTimes)
}
