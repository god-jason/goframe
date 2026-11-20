package ctyun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/god-jason/goframe/cloud"
	"github.com/god-jason/goframe/notify"
)

// Sms 是天翼云云通信短信服务客户端。
type Sms struct {
	cfg    Config
	client *http.Client
}

// 确认天翼云短信客户端实现统一云接口。
var _ cloud.Sms = (*Sms)(nil)

// NewSms 按配置创建短信客户端。
func NewSms(cfg Config) (*Sms, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Sms{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// smsResponse 是短信接口的 JSON 响应；
// code 成功时为字符串 "OK"，失败时可能为数字错误码。
type smsResponse struct {
	Code      json.RawMessage `json:"code"`
	Message   string          `json:"message"`
	RequestID string          `json:"requestId"`
}

func (r smsResponse) codeString() string {
	return strings.Trim(string(r.Code), `"`)
}

// Send 使用签名与模板 CODE 向号码列表发送短信。
// 天翼云接口单次仅支持一个接收号码，本方法对号码逐个下发，任一失败即返回错误。
// params 为模板变量（JSON 对象格式，如 {"code":"123"}）。
func (s *Sms) Send(ctx context.Context, phoneNumbers []string, signName, templateCode string, params map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(phoneNumbers) == 0 {
		return fmt.Errorf("ctyun: sms requires at least one phone number")
	}
	if strings.TrimSpace(templateCode) == "" {
		return fmt.Errorf("ctyun: sms template_code is required")
	}
	if strings.TrimSpace(signName) == "" {
		signName = s.cfg.SMS.SignName
	}
	templateParam := ""
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("ctyun: encode sms template param: %w", err)
		}
		templateParam = string(raw)
	}
	for _, phone := range phoneNumbers {
		if err := s.sendOne(ctx, phone, signName, templateCode, templateParam); err != nil {
			return err
		}
	}
	return nil
}

// sendOne 下发单个号码的模板短信。
func (s *Sms) sendOne(ctx context.Context, phone, signName, templateCode, templateParam string) error {
	payload := map[string]string{
		"action":       "SendSms",
		"phoneNumber":  phone,
		"signName":     signName,
		"templateCode": templateCode,
	}
	if templateParam != "" {
		payload["templateParam"] = templateParam
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ctyun: encode sms request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.SMS.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ctyun: build sms request: %w", err)
	}
	requestID := uuid.NewString()
	eopDate, authorization := eopSign(s.cfg.AccessKeyID, s.cfg.SecretAccessKey, requestID, req.URL, body, nowFunc())
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("ctyun-eop-request-id", requestID)
	req.Header.Set("Eop-date", eopDate)
	req.Header.Set("Eop-Authorization", authorization)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("ctyun: send sms: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("ctyun: read sms response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ctyun: send sms: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result smsResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("ctyun: decode sms response: %w: %s", err, strings.TrimSpace(string(raw)))
	}
	if result.codeString() != "OK" {
		return fmt.Errorf("ctyun: send sms failed: code %s: %s (request %s)",
			result.codeString(), result.Message, result.RequestID)
	}
	return nil
}

// SendSms 实现 cloud.Sms 统一接口；
// req.Sign 为空时使用配置中的短信签名，req.Params 作为模板变量。
func (s *Sms) SendSms(ctx context.Context, req *cloud.SmsRequest) error {
	if req == nil {
		return fmt.Errorf("ctyun: sms request is nil")
	}
	return s.Send(ctx, req.PhoneNumbers, req.Sign, req.TemplateID, req.Params)
}

// SmsProvider 把天翼云短信适配为 notify 的短信服务商。
//
// notify 消息的 Metadata 约定：
//   - template_code：短信模板 CODE（必填）；
//   - 其余键值对作为模板变量传入 templateParam（JSON 对象）。
type SmsProvider struct {
	sms *Sms
}

var _ notify.Provider = (*SmsProvider)(nil)

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
		return fmt.Errorf("ctyun: sms metadata requires template_code")
	}
	return p.sms.Send(ctx, to, p.sms.cfg.SMS.SignName, templateCode, vars)
}
