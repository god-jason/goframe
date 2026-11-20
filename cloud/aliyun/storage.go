package aliyun

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// Storage 是阿里云 OSS 对象存储客户端（绑定默认 Bucket）。
type Storage struct {
	client *oss.Client
	bucket *oss.Bucket
	cfg    StorageConfig
}

// NewStorage 按配置创建 OSS 客户端。
func NewStorage(cfg Config) (*Storage, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := oss.New(cfg.Storage.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret,
		oss.Timeout(int64(cfg.Timeout/time.Second), int64(cfg.Timeout/time.Second)))
	if err != nil {
		return nil, fmt.Errorf("aliyun: create oss client: %w", err)
	}
	bucket, err := client.Bucket(cfg.Storage.Bucket)
	if err != nil {
		return nil, fmt.Errorf("aliyun: open oss bucket: %w", err)
	}
	return &Storage{client: client, bucket: bucket, cfg: cfg.Storage}, nil
}

// Client 返回底层 OSS 客户端，便于操作其他 Bucket。
func (s *Storage) Client() *oss.Client { return s.client }

// Bucket 返回默认存储桶。
func (s *Storage) Bucket() *oss.Bucket { return s.bucket }

// Put 上传对象。
func (s *Storage) Put(ctx context.Context, key string, body io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.bucket.PutObject(key, body); err != nil {
		return fmt.Errorf("aliyun: oss put %s: %w", key, err)
	}
	return nil
}

// Get 下载对象，调用方负责关闭返回的 ReadCloser。
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := s.bucket.GetObject(key)
	if err != nil {
		return nil, fmt.Errorf("aliyun: oss get %s: %w", key, err)
	}
	return body, nil
}

// Delete 删除对象。
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.bucket.DeleteObject(key); err != nil {
		return fmt.Errorf("aliyun: oss delete %s: %w", key, err)
	}
	return nil
}

// SignURL 生成带有效期的下载链接。
func (s *Storage) SignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	url, err := s.bucket.SignURL(key, oss.HTTPGet, int64(expiry/time.Second))
	if err != nil {
		return "", fmt.Errorf("aliyun: oss sign url %s: %w", key, err)
	}
	return url, nil
}
