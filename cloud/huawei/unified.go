package huawei

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/god-jason/goframe/cloud"
)

// 确认华为云客户端实现统一云接口。
var (
	_ cloud.Sms     = (*Sms)(nil)
	_ cloud.Voice   = (*Voice)(nil)
	_ cloud.Storage = (*Storage)(nil)
)

// SendSms 实现 cloud.Sms 统一接口；
// 签名使用配置中的 signature，模板变量序列化为 JSON 数组传入。
func (s *Sms) SendSms(ctx context.Context, req *cloud.SmsRequest) error {
	if req == nil {
		return fmt.Errorf("huawei: sms request is nil")
	}
	paras := ""
	if list := cloud.SmsTemplateParams(req); len(list) > 0 {
		raw, err := json.Marshal(list)
		if err != nil {
			return fmt.Errorf("huawei: encode sms template paras: %w", err)
		}
		paras = string(raw)
	}
	return s.Send(ctx, req.PhoneNumbers, req.TemplateID, paras)
}

// PlayVoice 实现 cloud.Voice 统一接口，通过 VCP 语音通话播报文本；
// 华为云语音使用直接文本（req.Message），req.TemplateID 被忽略。
func (v *Voice) PlayVoice(ctx context.Context, req *cloud.VoiceRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("huawei: voice request is nil")
	}
	if req.Message == "" {
		return "", fmt.Errorf("huawei: voice message is required")
	}
	return v.Call(ctx, req.Callee, req.Message)
}
