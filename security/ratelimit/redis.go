package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// defaultRedisPrefix 是 Redis 限流 key 的默认前缀。
const defaultRedisPrefix = "goframe:ratelimit:"

// redisTokenBucketScript 在 Redis 内完成一次令牌桶判定，单次往返且原子执行。
// KEYS[1]=桶 key，ARGV[1]=每秒令牌数，ARGV[2]=桶容量，
// ARGV[3]=当前毫秒时间戳，ARGV[4]=key 过期毫秒数。
const redisTokenBucketScript = `
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])

local tokens = tonumber(redis.call("HGET", KEYS[1], "tokens"))
local ts = tonumber(redis.call("HGET", KEYS[1], "ts"))
if not tokens or not ts then
	tokens = burst
	ts = now
end

local delta = now - ts
if delta < 0 then
	delta = 0
end
tokens = math.min(burst, tokens + (delta / 1000.0) * rate)

local allowed = 0
if tokens >= 1 then
	tokens = tokens - 1
	allowed = 1
end

redis.call("HSET", KEYS[1], "tokens", tostring(tokens), "ts", tostring(now))
redis.call("PEXPIRE", KEYS[1], ttl)
return allowed
`

// RedisLimiter 是基于 Redis 的分布式限流器，多个实例共享同一份令牌桶状态。
type RedisLimiter struct {
	client   redis.UniversalClient
	limit    float64
	burst    int
	prefix   string
	failOpen bool
}

var _ Limiter = (*RedisLimiter)(nil)

// NewRedisLimiter 创建 Redis 限流器。
// limit 为每秒补充的令牌数，burst 为桶容量；prefix 为空时使用默认前缀。
// failOpen 为 true 时 Redis 故障将放行请求，否则返回错误并拒绝请求。
func NewRedisLimiter(client redis.UniversalClient, limit float64, burst int, prefix string, failOpen bool) *RedisLimiter {
	if limit <= 0 {
		limit = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if prefix == "" {
		prefix = defaultRedisPrefix
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &RedisLimiter{
		client:   client,
		limit:    limit,
		burst:    burst,
		prefix:   prefix,
		failOpen: failOpen,
	}
}

// key 拼接实际使用的 Redis key，便于统一前缀与测试。
func (l *RedisLimiter) key(key string) string { return l.prefix + key }

// Allow 判断请求是否被允许。
func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if l == nil || l.client == nil {
		return false, fmt.Errorf("ratelimit: redis client is nil")
	}
	if key == "" {
		return false, fmt.Errorf("ratelimit: key must not be empty")
	}

	// 桶在最后一次访问后保留足够长时间，使令牌能完全回满，避免状态被过早清理。
	ttl := int64(float64(l.burst)/l.limit*1000) + 1000
	allowed, err := l.client.Eval(ctx, redisTokenBucketScript, []string{l.key(key)},
		l.limit, l.burst, time.Now().UnixMilli(), ttl,
	).Int()
	if err != nil {
		if l.failOpen {
			return true, nil
		}
		return false, fmt.Errorf("ratelimit: redis eval: %w", err)
	}
	return allowed == 1, nil
}
