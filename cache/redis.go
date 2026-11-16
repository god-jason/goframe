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
