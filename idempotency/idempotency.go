// Package idempotency 提供基于 Idempotency-Key 请求头的 HTTP 幂等中间件与记录存储。
package idempotency

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

const (
	// HeaderIdempotencyKey 是幂等键请求头。
	HeaderIdempotencyKey = "Idempotency-Key"
	// maxIdempotencyKeyLength 限制幂等键长度，避免超长键拖垮存储。
	maxIdempotencyKeyLength = 128
	// defaultIdempotencyTTL 是未指定 TTL 时的缓存时长。
	defaultIdempotencyTTL = 24 * time.Hour
)

// Record 是幂等请求的缓存记录。
type Record struct {
	// Status 是 HTTP 状态码；InProgress 时为 0。
	Status int
	// Header 是响应头快照。
	Header http.Header
	// Body 是响应体快照。
	Body []byte
	// CreatedAt 是记录创建时间。
	CreatedAt time.Time
	// InProgress 表示同 key 请求正在执行。
	InProgress bool
}

// Store 是幂等记录存储。多实例部署时应使用 Redis 等共享存储，
// 并在存储层保证 Set 的原子性，否则进程间仍可能出现并发重复执行。
type Store interface {
	Get(ctx context.Context, key string) (*Record, error)
	Set(ctx context.Context, key string, record *Record, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// MemoryStore 是基于内存的 Store 实现，仅适用于单实例部署与测试。
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

type memoryEntry struct {
	record    *Record
	expiresAt time.Time
}

// NewMemoryStore 创建内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]memoryEntry)}
}

// Get 读取未过期的记录，不存在时返回 (nil, nil)。
func (s *MemoryStore) Get(ctx context.Context, key string) (*Record, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return nil, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(s.entries, key)
		return nil, nil
	}
	return cloneRecord(entry.record), nil
}

// Set 写入记录。ttl <= 0 表示不过期。
func (s *MemoryStore) Set(ctx context.Context, key string, record *Record, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("idempotency: record must not be nil")
	}

	entry := memoryEntry{record: cloneRecord(record)}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	s.mu.Lock()
	s.entries[key] = entry
	s.mu.Unlock()
	return nil
}

// Delete 删除记录。
func (s *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return nil
}

// Middleware 为 POST/PUT/PATCH 请求提供幂等处理。
// 客户端通过 Idempotency-Key 头标识请求；相同 key 的已完成请求会直接回放缓存响应，
// 正在执行的相同 key 返回 409。5xx 响应不缓存，允许客户端重试。
func Middleware(store Store, ttl time.Duration) gin.HandlerFunc {
	if store == nil {
		return func(c *gin.Context) { c.Next() }
	}
	if ttl <= 0 {
		ttl = defaultIdempotencyTTL
	}
	locks := newKeyLocker()

	return func(c *gin.Context) {
		if !idempotentMethod(c.Request.Method) {
			c.Next()
			return
		}

		key := strings.TrimSpace(c.GetHeader(HeaderIdempotencyKey))
		if key == "" {
			c.Next()
			return
		}
		if len(key) > maxIdempotencyKeyLength {
			api.Fail(c, apperrors.BadRequest("idempotency key is too long"))
			return
		}

		ctx := c.Request.Context()
		// 仅用锁保护“检查 + 占位”，执行阶段不持锁，避免同 key 请求串行等待。
		unlock := locks.lock(key)
		record, err := store.Get(ctx, key)
		if err != nil {
			unlock()
			api.Fail(c, apperrors.Unavailable("idempotency store unavailable"))
			return
		}
		if record != nil {
			unlock()
			if record.InProgress {
				c.Header("Retry-After", "1")
				api.Fail(c, apperrors.Conflict("a request with the same idempotency key is in progress"))
				return
			}
			replayRecord(c, record)
			return
		}
		if err := store.Set(ctx, key, &Record{
			Header:     make(http.Header),
			CreatedAt:  time.Now(),
			InProgress: true,
		}, ttl); err != nil {
			unlock()
			api.Fail(c, apperrors.Unavailable("idempotency store unavailable"))
			return
		}
		unlock()

		completed := false
		original := c.Writer
		writer := &idempotencyWriter{ResponseWriter: original}
		c.Writer = writer
		defer func() {
			// panic 时也要还原 writer，让外层 Recovery 能写到真实连接。
			c.Writer = original
		}()
		defer func() {
			if !completed {
				// 处理器 panic 或未完成时删除占位，允许客户端用同一 key 重试。
				_ = store.Delete(context.Background(), key)
			}
		}()

		c.Next()
		c.Writer = original

		if c.IsAborted() && !writer.written {
			completed = true
			_ = store.Delete(ctx, key)
			return
		}

		result := &Record{
			Status:    writer.Status(),
			Header:    cloneHeader(original.Header()),
			Body:      append([]byte(nil), writer.body.Bytes()...),
			CreatedAt: time.Now(),
		}
		completed = true

		if result.Status >= http.StatusInternalServerError {
			// 服务端错误不作为最终结果缓存，避免客户端重试永远拿到失败响应。
			_ = store.Delete(ctx, key)
		} else if err := store.Set(ctx, key, result, ttl); err != nil {
			// 缓存失败不影响本次响应，后续请求会重新执行。
		}

		writeRecord(original, result)
	}
}

// replayRecord 回放缓存响应。
func replayRecord(c *gin.Context, record *Record) {
	header := c.Writer.Header()
	for key := range record.Header {
		header.Del(key)
	}
	for key, values := range record.Header {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	writeRecord(c.Writer, record)
	c.Abort()
}

// writeRecord 写出记录的状态码与响应体。
func writeRecord(w gin.ResponseWriter, record *Record) {
	status := record.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(record.Body) > 0 {
		_, _ = w.Write(record.Body)
	}
}

// idempotentMethod 判断方法是否支持幂等键。
func idempotentMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// idempotencyWriter 缓冲响应，待处理器完成后统一写出并缓存。
type idempotencyWriter struct {
	gin.ResponseWriter

	body    bytes.Buffer
	status  int
	written bool
}

func (w *idempotencyWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.status = code
	w.written = true
}

func (w *idempotencyWriter) WriteHeaderNow() {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
}

func (w *idempotencyWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	return w.body.Write(p)
}

func (w *idempotencyWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *idempotencyWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *idempotencyWriter) Size() int { return w.body.Len() }

func (w *idempotencyWriter) Written() bool { return w.written }

// Flush 不做任何事：响应需要完整捕获后才能缓存与回放。
func (w *idempotencyWriter) Flush() {}

// Hijack 不支持：接管连接会绕过响应缓存。
func (w *idempotencyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("idempotency: hijack is not supported")
}

func (w *idempotencyWriter) CloseNotify() <-chan bool { return w.ResponseWriter.CloseNotify() }

func (w *idempotencyWriter) Pusher() http.Pusher { return w.ResponseWriter.Pusher() }

// keyLocker 是同 key 请求的进程内互斥锁，用于让“检查 + 占位”原子化。
type keyLocker struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

func newKeyLocker() *keyLocker {
	return &keyLocker{locks: make(map[string]*keyLock)}
}

// lock 获取指定 key 的锁，返回释放函数。
func (l *keyLocker) lock(key string) func() {
	l.mu.Lock()
	entry := l.locks[key]
	if entry == nil {
		entry = &keyLock{}
		l.locks[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}

// contextErr 把 ctx 取消转换为带前缀的错误。
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("idempotency: %w", err)
	}
	return nil
}

// cloneRecord 复制记录，避免存储与调用方共享可变数据。
func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Header = cloneHeader(record.Header)
	clone.Body = append([]byte(nil), record.Body...)
	return &clone
}

// cloneHeader 复制响应头。
func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	clone := make(http.Header, len(header))
	for key, values := range header {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
