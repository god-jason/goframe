package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIError 表示服务端返回了非 2xx 状态码。
type APIError struct {
	Status int
	Body   string
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("httpclient: unexpected status %d", e.Status)
	}
	return fmt.Sprintf("httpclient: unexpected status %d: %s", e.Status, e.Body)
}

// GetJSON 发送 GET 请求并把 JSON 响应解码到 out。out 为 nil 时只校验状态码。
func (c *Client) GetJSON(ctx context.Context, url string, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("httpclient: create GET request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return c.doJSON(req, out)
}

// PostJSON 发送 JSON 请求体并把 JSON 响应解码到 out。in 为 nil 时不发送请求体。
func (c *Client) PostJSON(ctx context.Context, url string, in, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("httpclient: encode request body: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("httpclient: create POST request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doJSON(req, out)
}

// doJSON 执行请求、校验状态码并解码响应。
func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.Do(req.Context(), req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if readErr != nil {
			return fmt.Errorf("httpclient: read error response: %w", readErr)
		}
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("httpclient: decode response: %w", err)
	}
	return nil
}
