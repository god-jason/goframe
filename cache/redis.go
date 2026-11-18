package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache 是基于 Redis 的缓存实现。
type RedisCache struct {
	client *redis.Client
	prefix string
}

// NewRedis 创建 Redis 缓存并检查连通性。
func NewRedis(cfg Config) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: redis ping: %w", err)
	}
	return &RedisCache{client: client, prefix: cfg.KeyPrefix}, nil
}

func (c *RedisCache) key(key string) string { return c.prefix + key }

// Get 读取缓存并反序列化到 value（value 必须为指针）。
func (c *RedisCache) Get(ctx context.Context, key string, value any) error {
	data, err := c.client.Get(ctx, c.key(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return ErrMiss
		}
		return fmt.Errorf("cache: get %q: %w", key, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("cache: decode %q: %w", key, err)
	}
	return nil
}

// Set 写入缓存，ttl<=0 表示不过期。
func (c *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: encode %q: %w", key, err)
	}
	if err := c.client.Set(ctx, c.key(key), data, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Delete 删除缓存。
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.key(key)).Err()
}

// Exists 判断缓存是否存在。
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, c.key(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Ping 检查 Redis 连通性。
func (c *RedisCache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

// Close 关闭 Redis 连接。
func (c *RedisCache) Close() error { return c.client.Close() }

// Client 返回底层 Redis 客户端，供锁、限流、Pub/Sub 等扩展能力复用连接。
func (c *RedisCache) Client() *redis.Client { return c.client }

// Publish 向 Redis 频道发布消息。
func (c *RedisCache) Publish(ctx context.Context, channel string, payload []byte) error {
	if channel == "" {
		return fmt.Errorf("cache: channel must not be empty")
	}
	if err := c.client.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("cache: publish %q: %w", channel, err)
	}
	return nil
}

// Subscribe 订阅一个或多个 Redis 频道，调用方需负责关闭返回的 PubSub。
func (c *RedisCache) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.client.Subscribe(ctx, channels...)
}

// Eval 执行 Lua 脚本。keys 为脚本访问的 Redis key，args 为附加参数。
func (c *RedisCache) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	result, err := c.client.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: eval: %w", err)
	}
	return result, nil
}
