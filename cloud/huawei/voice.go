package huawei

import (
	"context"
	"fmt"
	"net/http"

	httpclient "github.com/huaweicloud/huaweicloud-sdk-go-v3/core"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/def"
)

// ExecuteCallBody 是 VCP 1.0 语音呼叫请求体。
type ExecuteCallBody struct {
	Number      string `json:"number"`
	ShowNumber  string `json:"showNumber,omitempty"`
	PlayType    string `json:"playType,omitempty"`
	TTSMessage  string `json:"ttsMessage,omitempty"`
	PlayTimes   int    `json:"playTimes,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
}

// ExecuteCallResponse 是 VCP 1.0 语音呼叫响应。
// HttpStatusCode 由华为云 SDK 反序列化时通过反射回填，字段缺失会导致 2xx 响应 panic。
type ExecuteCallResponse struct {
	HttpStatusCode int    `json:"-"`
	StatusCode     int    `json:"statusCode,omitempty"`
	Message        string `json:"message,omitempty"`
	CallID         string `json:"callId,omitempty"`
}

// executeCallRequest 承载请求体字段，字段名与 API 定义中的 Body 对应。
type executeCallRequest struct {
	Body *ExecuteCallBody `json:"body,omitempty"`
}

// Voice 是华为云语音通话（VCP）客户端。
type Voice struct {
	hc  *httpclient.HcHttpClient
	cfg Config
}

// NewVoice 按配置创建 VCP 语音客户端。
func NewVoice(cfg Config) (*Voice, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	hc, err := httpclient.NewHcHttpClientBuilder().
		WithEndpoint(cfg.vcpEndpoint()).
		WithCredential(newCredentials(cfg)).
		WithHttpConfig(config.DefaultHttpConfig().WithTimeout(cfg.Timeout)).
		SafeBuild()
	if err != nil {
		return nil, fmt.Errorf("huawei: create vcp client: %w", err)
	}
	return &Voice{hc: hc, cfg: cfg}, nil
}

// Call 使用文本转语音向被叫号码发起语音呼叫，返回通话任务 ID。
func (v *Voice) Call(ctx context.Context, callee, message string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if callee == "" || message == "" {
		return "", fmt.Errorf("huawei: voice call requires callee and message")
	}
	playTimes := v.cfg.Voice.PlayTimes
	if playTimes <= 0 {
		playTimes = 1
	}
	body := &ExecuteCallBody{
		Number:      callee,
		ShowNumber:  v.cfg.Voice.ShowNumber,
		PlayType:    "tts",
		TTSMessage:  message,
		PlayTimes:   playTimes,
		CountryCode: v.cfg.Voice.CountryCode,
	}
	// 每次调用构造独立请求定义：SDK 会把响应反序列化进 WithResponse 的共享实例，
	// 复用 reqDef 会导致并发调用数据竞争。
	reqDef := def.NewHttpRequestDefBuilder().
		WithMethod(http.MethodPost).
		WithPath("/vcp/executeCall").
		WithContentType("application/json").
		WithRequestField(def.NewFieldDef().WithName("Body").WithLocationType(def.Body)).
		WithResponse(new(ExecuteCallResponse)).
		Build()
	resp, err := v.hc.Sync(&executeCallRequest{Body: body}, reqDef)
	if err != nil {
		return "", fmt.Errorf("huawei: voice call: %w", err)
	}
	result, ok := resp.(*ExecuteCallResponse)
	if !ok || result == nil {
		return "", fmt.Errorf("huawei: voice call: unexpected response")
	}
	if result.StatusCode != 0 {
		return "", fmt.Errorf("huawei: voice call: status %d: %s", result.StatusCode, result.Message)
	}
	return result.CallID, nil
}
