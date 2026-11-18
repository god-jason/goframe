// Package httpclient 提供带重试、熔断与响应大小限制的通用 HTTP 客户端。
package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

// ErrCircuitOpen 表示熔断器已打开，请求被快速拒绝。
var ErrCircuitOpen = errors.New("httpclient: circuit breaker is open")

// Options 是客户端配置。零值字段会应用默认值。
type Options struct {
	// Timeout 是单次请求（含重定向）的总超时时间。
	Timeout time.Duration
	// MaxRetries 是失败后的最大重试次数，不含首次请求。
	MaxRetries int
	// RetryWaitMin 是首次重试的最小等待时间，后续按指数增长。
	RetryWaitMin time.Duration
	// RetryWaitMax 是单次重试等待时间上限。
	RetryWaitMax time.Duration
	// MaxResponseBytes 是响应体最大字节数，超过时读取返回错误。
	MaxResponseBytes int64
	// Transport 是可选的底层传输，默认使用 http.DefaultTransport。
	Transport http.RoundTripper
	// Breaker 是可选的熔断器，为空时不启用熔断。
	Breaker *Breaker
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	if o.RetryWaitMin <= 0 {
		o.RetryWaitMin = 100 * time.Millisecond
	}
	if o.RetryWaitMax <= 0 {
		o.RetryWaitMax = 2 * time.Second
	}
	if o.RetryWaitMax < o.RetryWaitMin {
		o.RetryWaitMax = o.RetryWaitMin
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = 10 << 20
	}
	return o
}

// Client 是通用 HTTP 客户端，可安全并发使用。
type Client struct {
	http *http.Client
	opts Options
}

// New 创建客户端。
func New(opts Options) *Client {
	opts = opts.withDefaults()
	return &Client{
		http: &http.Client{
			Timeout:   opts.Timeout,
			Transport: opts.Transport,
		},
		opts: opts,
	}
}

// Do 发送请求。仅对幂等方法与 429/5xx 响应做指数退避加抖动重试，等待过程遵守 ctx。
// 返回 4xx/5xx 时 error 为 nil，调用方需自行检查状态码。
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("httpclient: request must not be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("httpclient: request canceled: %w", err)
	}

	base, err := prepareRequest(req.Clone(ctx))
	if err != nil {
		return nil, err
	}

	breaker := c.opts.Breaker
	if breaker != nil && !breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	attempts := c.opts.MaxRetries + 1
	if !retryableMethod(base.Method) {
		attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := wait(ctx, c.backoff(attempt)); err != nil {
				c.recordFailure(breaker, err)
				return nil, fmt.Errorf("httpclient: retry wait: %w", err)
			}
		}

		attemptReq, err := replayRequest(base)
		if err != nil {
			c.recordFailure(breaker, err)
			return nil, err
		}

		resp, err := c.http.Do(attemptReq)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, fmt.Errorf("httpclient: do request: %w", ctx.Err())
			}
			continue
		}

		if resp.Body != nil {
			resp.Body = &limitedBody{body: resp.Body, remaining: c.opts.MaxResponseBytes, limit: c.opts.MaxResponseBytes}
		}

		if !retryableStatus(resp.StatusCode) || attempt == attempts-1 {
			c.recordResult(breaker, resp.StatusCode)
			return resp, nil
		}

		// 丢弃并关闭中间响应，避免连接泄漏。
		drain(resp.Body)
		lastErr = fmt.Errorf("httpclient: unexpected status %d", resp.StatusCode)
	}

	c.recordFailure(breaker, lastErr)
	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return nil, fmt.Errorf("httpclient: request failed after %d attempts: %w", attempts, lastErr)
}

// prepareRequest 确保请求体可重放。
func prepareRequest(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Body == http.NoBody || req.GetBody != nil {
		return req, nil
	}

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("httpclient: read request body: %w", err)
	}
	_ = req.Body.Close()

	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
	return req, nil
}

// replayRequest 为每次尝试创建独立请求和请求体。
func replayRequest(base *http.Request) (*http.Request, error) {
	clone := base.Clone(base.Context())
	if base.Body == nil || base.Body == http.NoBody {
		return clone, nil
	}
	if base.GetBody == nil {
		return nil, fmt.Errorf("httpclient: request body is not replayable")
	}
	body, err := base.GetBody()
	if err != nil {
		return nil, fmt.Errorf("httpclient: recreate request body: %w", err)
	}
	clone.Body = body
	return clone, nil
}

// backoff 计算第 retry 次重试的等待时间（从 1 开始），使用 full jitter。
func (c *Client) backoff(retry int) time.Duration {
	wait := c.opts.RetryWaitMin
	for i := 1; i < retry; i++ {
		if wait >= c.opts.RetryWaitMax/2 {
			wait = c.opts.RetryWaitMax
			break
		}
		wait *= 2
	}
	if wait > c.opts.RetryWaitMax {
		wait = c.opts.RetryWaitMax
	}
	if wait <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(wait))) + 1
}

// wait 等待指定时间，ctx 取消时立即返回。
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableMethod 判断方法是否幂等。非幂等方法不重试，避免重复副作用。
func retryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// retryableStatus 判断状态码是否值得重试。
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// recordResult 根据最终状态码更新熔断器。
func (c *Client) recordResult(breaker *Breaker, status int) {
	if breaker == nil {
		return
	}
	if retryableStatus(status) {
		breaker.Failure()
		return
	}
	breaker.Success()
}

// recordFailure 记录传输层失败，主动取消不计入熔断统计。
func (c *Client) recordFailure(breaker *Breaker, err error) {
	if breaker == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	breaker.Failure()
}

// drain 读取少量剩余数据并关闭响应体，便于连接复用。
func drain(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4<<10))
	_ = body.Close()
}

// limitedBody 限制响应体最大读取字节数。
type limitedBody struct {
	body      io.ReadCloser
	remaining int64
	limit     int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, fmt.Errorf("httpclient: response body exceeds %d bytes", b.limit)
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.body.Close() }
