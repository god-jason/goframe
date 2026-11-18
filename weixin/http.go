package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxResponseSize 限制微信响应体大小。
const maxResponseSize = 1 << 20

// APIError 是微信接口返回的错误。
type APIError struct {
	// Code 是微信 errcode。
	Code int
	// Message 是微信 errmsg。
	Message string
	// Status 是 HTTP 状态码。
	Status int
	// Path 是请求路径。
	Path string
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	return fmt.Sprintf("weixin: %s status=%d errcode=%d errmsg=%s", e.Path, e.Status, e.Code, e.Message)
}

// TokenInvalid 判断错误是否表示 access_token 失效。
func (e *APIError) TokenInvalid() bool {
	switch e.Code {
	case 40001, 40014, 42001:
		return true
	default:
		return false
	}
}

func urlQueryEscape(value string) string { return url.QueryEscape(value) }

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, out)
}

// doJSON 发送请求并解析微信响应。
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("weixin: client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("weixin: encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("weixin: create request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("weixin: request %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("weixin: read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &APIError{
			Status:  response.StatusCode,
			Message: strings.TrimSpace(string(data)),
			Path:    path,
		}
	}

	var errorResponse struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &errorResponse); err != nil {
			return fmt.Errorf("weixin: decode response: %w", err)
		}
		if errorResponse.ErrCode != 0 {
			return &APIError{
				Code:    errorResponse.ErrCode,
				Message: errorResponse.ErrMsg,
				Status:  response.StatusCode,
				Path:    path,
			}
		}
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("weixin: decode response: %w", err)
		}
	}
	return nil
}
