package onenet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/god-jason/goframe/cloud"
	"github.com/god-jason/goframe/notify"
)

// Sms 是 OneNET 短信能力平台客户端。
type Sms struct {
	cfg    Config
	client *http.Client
}

// 确认 OneNET 短信客户端实现统一云接口。
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

// smsResult 是短信下发接口的 JSON 响应。
type smsResult struct {
	Result     string `json:"result"`
	ResultInfo string `json:"resultInfo"`
	ErrorInfo  string `json:"errorInfo"`
}

// 下发结果码（tempSignSmsSend）。
const (
	codeSuccess     = "11101" // 下发处理成功
	codePartialBlack = "11107" // 部分接收号码在黑名单，部分成功
	codePartialRate  = "11121" // 部分接收号码超频，部分成功
)

// Send 使用签名 ID 与模板向号码列表下发模板短信，返回下发流水号。
// params 为模板自定义参数（模板中以 #参数名# 定义）。
func (s *Sms) Send(ctx context.Context, phoneNumbers []string, signID, templateID string, params map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(phoneNumbers) == 0 {
		return "", fmt.Errorf("onenet: sms requires at least one phone number")
	}
	if strings.TrimSpace(templateID) == "" {
		return "", fmt.Errorf("onenet: sms template_id is required")
	}
	if strings.TrimSpace(signID) == "" {
		signID = s.cfg.SMS.SignID
	}
	if strings.TrimSpace(signID) == "" {
		return "", fmt.Errorf("onenet: sms sign_id is required")
	}

	query := url.Values{}
	query.Set("sicode", s.cfg.SIcode)
	query.Set("tempid", templateID)
	query.Set("signId", signID)
	query.Set("mobiles", strings.Join(phoneNumbers, ","))
	for k, v := range params {
		query.Set(k, v)
	}

	endpoint := strings.TrimRight(s.cfg.Endpoint, "/") + "/tempSignSmsSend?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("onenet: build sms request: %w", err)
	}
	req.Header.Set("Accept", "text/plain;charset=utf-8")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("onenet: send sms: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("onenet: read sms response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("onenet: send sms: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result smsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("onenet: decode sms response: %w: %s", err, strings.TrimSpace(string(raw)))
	}
	switch result.Result {
	case codeSuccess, codePartialBlack, codePartialRate:
		// 部分号码因黑名单/频控被放弃时视为部分成功，流水号仍返回。
		return result.ResultInfo, nil
	default:
		return "", fmt.Errorf("onenet: send sms failed: result %s: %s %s",
			result.Result, result.ResultInfo, result.ErrorInfo)
	}
}

// SendSms 实现 cloud.Sms 统一接口；
// OneNET 模板按参数名取值，req.Params 生效，req.TemplateParams 被忽略。
func (s *Sms) SendSms(ctx context.Context, req *cloud.SmsRequest) error {
	if req == nil {
		return fmt.Errorf("onenet: sms request is nil")
	}
	_, err := s.Send(ctx, req.PhoneNumbers, req.Sign, req.TemplateID, req.Params)
	return err
}

// SmsProvider 把 OneNET 短信适配为 notify 的短信服务商。
//
// notify 消息的 Metadata 约定：
//   - template_id：模板 ID（必填）；
//   - sign_id：签名 ID（可选，缺省使用配置中的 sms.sign_id）；
//   - 其余键值对作为模板自定义参数传入。
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

// Send 实现 notify.Provider：to 为号码列表，params 携带 template_id 与模板参数。
// OneNET 仅支持模板短信，content 被忽略。
func (p *SmsProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	vars := make(map[string]string, len(params))
	templateID, signID := "", ""
	for k, v := range params {
		switch k {
		case "template_id":
			templateID = v
		case "sign_id":
			signID = v
		default:
			vars[k] = v
		}
	}
	if templateID == "" {
		return fmt.Errorf("onenet: sms metadata requires template_id")
	}
	_, err := p.sms.Send(ctx, to, signID, templateID, vars)
	return err
}
