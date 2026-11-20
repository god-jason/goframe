// Package cloud 定义与厂商无关的云服务统一能力接口。
//
// 各厂商包（cloud/aliyun、cloud/tencent、cloud/huawei、cloud/onenet、cloud/ctyun）
// 的具体客户端实现这些接口，业务代码可以只依赖接口类型，
// 从而在不改业务逻辑的前提下更换云厂商。
//
// 用法：
//
//	var sms cloud.Sms = aliyun.GetSms()
//	err := sms.SendSms(ctx, &cloud.SmsRequest{
//	    PhoneNumbers: []string{"13800000000"},
//	    TemplateID:   "SMS_001",
//	    Params:       map[string]string{"code": "1234"},
//	})
package cloud

import (
	"context"
	"io"
	"sort"
	"strconv"
	"time"
)

// SmsRequest 是统一短信发送请求。
// 不同厂商对签名与模板的表达不同：
// Sign 对应阿里云/腾讯云的签名名称、OneNET/天翼云的签名（ID 或名称）、
// 华为云使用配置中的签名；TemplateID 为模板 CODE / ID。
// 模板变量二选一：Params（键值对，适合 JSON 对象或按参数名取值的厂商，
// 如阿里云、OneNET、天翼云，键为 "0"、"1"… 时按数值序展开）或
// TemplateParams（显式有序列表，适合腾讯云、华为云等按序取参的厂商；非空时优先）。
type SmsRequest struct {
	PhoneNumbers   []string          `json:"phone_numbers"`
	Sign           string            `json:"sign,omitempty"`
	TemplateID     string            `json:"template_id"`
	Params         map[string]string `json:"params,omitempty"`
	TemplateParams []string          `json:"template_params,omitempty"`
}

// Sms 是统一短信发送接口。
type Sms interface {
	SendSms(ctx context.Context, req *SmsRequest) error
}

// VoiceRequest 是统一语音通知请求。
// TemplateID 为 TTS 模板（阿里云/腾讯云必填）；
// Message 为直接播报文本（华为云 VCP 使用）；
// ShowNumber 与 PlayTimes 为 0/空时使用厂商配置默认值。
type VoiceRequest struct {
	Callee     string            `json:"callee"`
	ShowNumber string            `json:"show_number,omitempty"`
	TemplateID string            `json:"template_id,omitempty"`
	Message    string            `json:"message,omitempty"`
	PlayTimes  int               `json:"play_times,omitempty"`
	Params     map[string]string `json:"params,omitempty"`
}

// Voice 是统一语音通知（语音单呼/语音验证码）接口，
// 返回厂商侧的通话任务 ID 或请求 ID。
type Voice interface {
	PlayVoice(ctx context.Context, req *VoiceRequest) (callID string, err error)
}

// Storage 是统一对象存储接口（OSS / COS / OBS / ZOS 等）。
// key 为对象键；SignURL 生成 expiry 内有效的预签名 GET 链接。
type Storage interface {
	Put(ctx context.Context, key string, body io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	SignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// OrderedParams 把模板变量展开为有序字符串列表：
// 所有键均为非负整数时按数值升序，否则按键名字典序。
// 供需要按序传参的厂商（腾讯云、华为云）适配使用。
func OrderedParams(params map[string]string) []string {
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	numeric := true
	for k := range params {
		keys = append(keys, k)
		if n, err := strconv.Atoi(k); err != nil || n < 0 {
			numeric = false
		}
	}
	if numeric {
		sort.Slice(keys, func(i, j int) bool {
			a, _ := strconv.Atoi(keys[i])
			b, _ := strconv.Atoi(keys[j])
			return a < b
		})
	} else {
		sort.Strings(keys)
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, params[k])
	}
	return out
}

// SmsTemplateParams 返回请求中生效的有序模板参数：
// TemplateParams 非空时优先，否则展开 Params。
func SmsTemplateParams(req *SmsRequest) []string {
	if req == nil {
		return nil
	}
	if len(req.TemplateParams) > 0 {
		return req.TemplateParams
	}
	return OrderedParams(req.Params)
}
