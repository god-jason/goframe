package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

// Timeout 为请求上下文设置超时。
// 超时且尚未写出响应时返回 503；包装的 ResponseWriter 会丢弃超时后的迟到写入，
// 避免处理器继续写响应导致的竞态或数据污染。
//
// 限制说明：
//   - Go 无法强制终止正在执行的 goroutine，处理器必须自行监听
//     c.Request.Context() 并在取消后尽快返回；
//   - 处理器不响应取消时，本中间件会等待其结束，但 503 会在超时点立即写出并
//     Flush 给客户端，客户端不必等待处理器退出；
//   - 处理器的 panic 仍沿正常调用链交给外层 Recovery 处理。
func Timeout(d time.Duration) gin.HandlerFunc {
	if d <= 0 {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)

		writer := &timeoutWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		body := timeoutPayload(c)

		// 独立定时器在超时点写出 503，并让后续迟到写入被丢弃。
		// 处理器仍在当前 goroutine 执行，避免并发使用非线程安全的 gin.Context。
		done := make(chan struct{})
		timerDone := make(chan struct{})
		go func() {
			defer close(timerDone)
			select {
			case <-ctx.Done():
				writer.timeout(body)
			case <-done:
			}
		}()

		c.Next()
		close(done)
		// 等待定时器退出，确保响应结束后不再有后台写入。
		<-timerDone
	}
}

// timeoutPayload 生成超时响应体，保持与统一响应格式一致。
func timeoutPayload(c *gin.Context) []byte {
	data, err := json.Marshal(api.Response[any]{
		Code:      apperrors.CodeUnavailable,
		Message:   "request timeout",
		RequestID: api.RequestID(c),
	})
	if err != nil {
		return []byte(`{"code":50300,"message":"request timeout"}`)
	}
	return data
}

// timeoutWriter 包装 gin.ResponseWriter，超时后丢弃迟到写入。
type timeoutWriter struct {
	gin.ResponseWriter

	mu       sync.Mutex
	timedOut bool
	written  bool
}

// WriteHeader 在未超时前透传状态码。
func (w *timeoutWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

// Write 在未超时前透传响应体。
func (w *timeoutWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	w.written = true
	return w.ResponseWriter.Write(p)
}

// WriteString 实现 gin.ResponseWriter。
func (w *timeoutWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// WriteHeaderNow 在未超时前立即写出响应头。
func (w *timeoutWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeaderNow()
}

// Flush 在未超时前透传刷新。
func (w *timeoutWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.written = true
	w.ResponseWriter.Flush()
}

// Status 返回底层响应状态码。
func (w *timeoutWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Status()
}

// Size 返回已写出的响应体字节数。
func (w *timeoutWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Size()
}

// Written 返回是否已经写出响应。
func (w *timeoutWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}

// Hijack 在未超时前透传连接接管。
func (w *timeoutWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return nil, nil, http.ErrHandlerTimeout
	}
	w.written = true
	return w.ResponseWriter.Hijack()
}

// CloseNotify 透传连接关闭通知。
func (w *timeoutWriter) CloseNotify() <-chan bool { return w.ResponseWriter.CloseNotify() }

// Pusher 透传 HTTP/2 Server Push。
func (w *timeoutWriter) Pusher() http.Pusher { return w.ResponseWriter.Pusher() }

// timeout 标记超时并在尚未写出响应时返回 503。
func (w *timeoutWriter) timeout(body []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	w.timedOut = true
	if w.written {
		return
	}
	w.written = true

	header := w.ResponseWriter.Header()
	header.Del("Content-Length")
	header.Set("Content-Type", "application/json; charset=utf-8")
	w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.ResponseWriter.Write(body)
	// 立即把 503 推给客户端，避免处理器不响应取消时客户端一直等待。
	w.ResponseWriter.Flush()
}
