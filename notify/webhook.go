package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const webhookDefaultTimeout = 10 * time.Second

// WebhookConfig 是 Webhook 发送配置。
type WebhookConfig struct {
	URL     string            `mapstructure:"url"`
	Secret  string            `mapstructure:"secret"`
	Headers map[string]string `mapstructure:"headers"`
	Timeout time.Duration     `mapstructure:"timeout"`
}

// WebhookSender 以 HTTP POST JSON 的方式发送消息。
type WebhookSender struct {
	url     string
	secret  string
	headers map[string]string
	client  *http.Client
}

var _ Sender = (*WebhookSender)(nil)

// NewWebhook 创建 Webhook 发送器，只校验配置，不发起请求。
func NewWebhook(cfg WebhookConfig) (*WebhookSender, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid webhook url: %w", err)
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("notify: invalid webhook url %q", rawURL)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = webhookDefaultTimeout
	}
	headers := make(map[string]string, len(cfg.Headers))
	for key, value := range cfg.Headers {
		headers[key] = value
	}
	return &WebhookSender{
		url:     rawURL,
		secret:  cfg.Secret,
		headers: headers,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

// Send 将消息以 JSON POST 到目标地址；配置 Secret 时附加 HMAC-SHA256 签名头 X-Signature。
func (s *WebhookSender) Send(ctx context.Context, msg Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("notify: encode webhook payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("notify: create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range s.headers {
		request.Header.Set(key, value)
	}
	if s.secret != "" {
		signature := hmac.New(sha256.New, []byte(s.secret))
		signature.Write(payload)
		request.Header.Set("X-Signature", hex.EncodeToString(signature.Sum(nil)))
	}

	response, err := s.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("notify: send webhook: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("notify: webhook returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return nil
}
