package aliyun

import (
	"context"
	"encoding/json"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/client"
	dyvmsapi "github.com/alibabacloud-go/dyvmsapi-20170525/v2/client"
	"github.com/alibabacloud-go/tea/tea"
)

// Voice 是阿里云语音通知客户端。
type Voice struct {
	client *dyvmsapi.Client
	cfg    Config
}

// NewVoice 按配置创建语音通知客户端。
func NewVoice(cfg Config) (*Voice, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	timeoutMs := int(cfg.Timeout / 1000)
	client, err := dyvmsapi.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(cfg.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AccessKeySecret),
		Endpoint:        tea.String(cfg.voiceEndpoint()),
		ReadTimeout:     tea.Int(timeoutMs),
		ConnectTimeout:  tea.Int(timeoutMs),
	})
	if err != nil {
		return nil, fmt.Errorf("aliyun: create voice client: %w", err)
	}
	return &Voice{client: client, cfg: cfg}, nil
}

// SingleCallByTts 发起语音验证码/文本转语音单呼，返回通话任务 ID。
// ttsCode 为审核通过的 TTS 模板 ID，params 为模板变量。
func (v *Voice) SingleCallByTts(ctx context.Context, calledNumber, ttsCode string, params map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	req := &dyvmsapi.SingleCallByTtsRequest{
		CalledNumber:     tea.String(calledNumber),
		CalledShowNumber: tea.String(v.cfg.Voice.CalledShowNumber),
		TtsCode:          tea.String(ttsCode),
	}
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			return "", fmt.Errorf("aliyun: encode tts param: %w", err)
		}
		req.TtsParam = tea.String(string(raw))
	}
	resp, err := v.client.SingleCallByTts(req)
	if err != nil {
		return "", fmt.Errorf("aliyun: single call by tts: %w", err)
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "OK" {
		return "", fmt.Errorf("aliyun: single call by tts: code %s: %s", code, tea.StringValue(resp.Body.Message))
	}
	return tea.StringValue(resp.Body.CallId), nil
}

// SingleCallByVoice 使用审核通过的语音文件发起单呼，返回通话任务 ID。
func (v *Voice) SingleCallByVoice(ctx context.Context, calledNumber, voiceCode string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	req := &dyvmsapi.SingleCallByVoiceRequest{
		CalledNumber:     tea.String(calledNumber),
		CalledShowNumber: tea.String(v.cfg.Voice.CalledShowNumber),
		VoiceCode:        tea.String(voiceCode),
	}
	resp, err := v.client.SingleCallByVoice(req)
	if err != nil {
		return "", fmt.Errorf("aliyun: single call by voice: %w", err)
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "OK" {
		return "", fmt.Errorf("aliyun: single call by voice: code %s: %s", code, tea.StringValue(resp.Body.Message))
	}
	return tea.StringValue(resp.Body.CallId), nil
}
