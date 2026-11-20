package huawei

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/def"
	smsapi "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/smsapi/v1"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/smsapi/v1/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/smsapi/v1/region"

	"encoding/json"
)

// Sms 是华为云短信服务客户端。
type Sms struct {
	client *smsapi.SMSApiClient
	cfg    Config
}

// newCredentials 构建 AK/SK 凭据。
func newCredentials(cfg Config) *basic.Credentials {
	builder := basic.NewCredentialsBuilder().
		WithAk(cfg.AccessKeyID).
		WithSk(cfg.SecretAccessKey)
	if pid := strings.TrimSpace(cfg.ProjectID); pid != "" {
		builder = builder.WithProjectId(pid)
	}
	return builder.Build()
}

// NewSms 按配置创建短信客户端。
func NewSms(cfg Config) (*Sms, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r, err := region.SafeValueOf(cfg.Region)
	if err != nil {
		return nil, fmt.Errorf("huawei: sms region: %w", err)
	}
	client := smsapi.NewSMSApiClient(smsapi.SMSApiClientBuilder().
		WithHttpConfig(config.DefaultHttpConfig().WithTimeout(cfg.Timeout)).
		WithRegion(r).
		WithCredential(smsapi.NewSMSApiCredentialsBuilder().
			WithAk(cfg.AccessKeyID).
			WithSk(cfg.SecretAccessKey).
			Build()).
		Build())
	return &Sms{client: client, cfg: cfg}, nil
}

// Send 使用签名与模板向号码列表发送短信。
// templateParas 为模板变量 JSON 数组字符串（如 ["123","5"]），无变量时传空串。
func (s *Sms) Send(ctx context.Context, phoneNumbers []string, templateID, templateParas string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(phoneNumbers) == 0 {
		return fmt.Errorf("huawei: sms requires at least one phone number")
	}
	if strings.TrimSpace(templateID) == "" {
		return fmt.Errorf("huawei: sms template_id is required")
	}
	body := &model.BatchSendSmsRequestBody{
		To:          def.NewMultiPart(strings.Join(phoneNumbers, ",")),
		Signature:   def.NewMultiPart(s.cfg.SMS.Signature),
		TemplateId:  def.NewMultiPart(templateID),
		From:        def.NewMultiPart(s.cfg.SMS.From),
	}
	if templateParas != "" {
		body.TemplateParas = def.NewMultiPart(templateParas)
	}
	resp, err := s.client.BatchSendSms(&model.BatchSendSmsRequest{Body: body})
	if err != nil {
		return fmt.Errorf("huawei: send sms: %w", err)
	}
	code := deref(resp.Code)
	if code != "" && code != "200000" {
		return fmt.Errorf("huawei: send sms: code %s: %s", code, deref(resp.Description))
	}
	return nil
}

// SmsProvider 把华为云短信适配为 notify 的短信服务商。
//
// notify 消息的 Metadata 约定：
//   - template_id：短信模板 ID（必填）；
//   - 其余键值对按 key 升序组成模板变量数组（对应模板中的 $1..$n）。
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

// Send 实现 notify.Provider：to 为号码列表，params 携带 template_id 与模板变量。
func (p *SmsProvider) Send(ctx context.Context, to []string, content string, params map[string]string) error {
	templateID := params["template_id"]
	if templateID == "" {
		return fmt.Errorf("huawei: sms metadata requires template_id")
	}
	var templateParas string
	if vars := smsTemplateVars(params); len(vars) > 0 {
		raw, err := json.Marshal(vars)
		if err != nil {
			return fmt.Errorf("huawei: encode sms template paras: %w", err)
		}
		templateParas = string(raw)
	}
	return p.sms.Send(ctx, to, templateID, templateParas)
}

// smsTemplateVars 提取模板变量值：key 全部为数字时按数值升序，否则按字典序。
func smsTemplateVars(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "template_id" {
			keys = append(keys, k)
		}
	}
	numeric := len(keys) > 0
	for _, k := range keys {
		if _, err := strconv.Atoi(k); err != nil {
			numeric = false
			break
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
	vars := make([]string, 0, len(keys))
	for _, k := range keys {
		vars = append(vars, params[k])
	}
	return vars
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
