package tencent

import (
	"context"
	"fmt"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	vmsapi "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vms/v20200902"
)

// Voice 是腾讯云语音通知客户端。
type Voice struct {
	client *vmsapi.Client
	cfg    Config
}

// NewVoice 按配置创建语音通知客户端。
func NewVoice(cfg Config) (*Voice, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	credential := common.NewCredential(cfg.SecretID, cfg.SecretKey)
	client, err := vmsapi.NewClient(credential, cfg.Region, profile.NewClientProfile())
	if err != nil {
		return nil, fmt.Errorf("tencent: create voice client: %w", err)
	}
	return &Voice{client: client, cfg: cfg}, nil
}

// SendTtsVoice 使用文本转语音模板发起语音通知，返回请求 ID。
// templateID 为审核通过的语音模板 ID；playTimes 为播放次数，0 使用配置默认值。
func (v *Voice) SendTtsVoice(ctx context.Context, calledNumber, templateID string, playTimes int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if playTimes <= 0 {
		playTimes = v.cfg.Voice.PlayTimes
	}
	if playTimes <= 0 {
		playTimes = 1
	}
	req := &vmsapi.SendTtsVoiceRequest{
		CalledNumber:  common.StringPtr(calledNumber),
		TemplateId:    common.StringPtr(templateID),
		VoiceSdkAppid: common.StringPtr(v.cfg.Voice.SdkAppID),
		PlayTimes:     common.Uint64Ptr(uint64(playTimes)),
	}
	resp, err := v.client.SendTtsVoiceWithContext(ctx, req)
	if err != nil {
		return "", fmt.Errorf("tencent: send tts voice: %w", err)
	}
	if resp.Response == nil {
		return "", fmt.Errorf("tencent: send tts voice: empty response")
	}
	return deref(resp.Response.RequestId), nil
}
