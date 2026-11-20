package ctyun

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/god-jason/goframe/cloud"
)

// Storage 是天翼云对象存储 ZOS 客户端（S3 兼容协议）。
type Storage struct {
	client *minio.Client
	bucket string
}

// 确认天翼云 ZOS 客户端实现统一云接口。
var _ cloud.Storage = (*Storage)(nil)

// NewStorage 按配置创建 ZOS 客户端。
func NewStorage(cfg Config) (*Storage, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	endpoint, secure := splitEndpoint(cfg.Storage.Endpoint, cfg.useSSL())
	if endpoint == "" {
		return nil, fmt.Errorf("ctyun: storage.endpoint is required")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("ctyun: create zos client: %w", err)
	}
	return &Storage{client: client, bucket: cfg.Storage.Bucket}, nil
}

// splitEndpoint 去除接入地址的 scheme 前缀，并据此推断是否使用 HTTPS。
func splitEndpoint(endpoint string, defaultSecure bool) (host string, secure bool) {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimPrefix(endpoint, "https://"), true
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimPrefix(endpoint, "http://"), false
	default:
		return endpoint, defaultSecure
	}
}

// Put 上传对象，大小未知时由 minio-go 自动分片。
func (s *Storage) Put(ctx context.Context, key string, body io.Reader) error {
	if _, err := s.client.PutObject(ctx, s.bucket, key, body, -1, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("ctyun: zos put %s: %w", key, err)
	}
	return nil
}

// Get 读取对象内容，调用方负责关闭返回的 ReadCloser。
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("ctyun: zos get %s: %w", key, err)
	}
	return obj, nil
}

// Delete 删除对象。
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("ctyun: zos delete %s: %w", key, err)
	}
	return nil
}

// SignURL 生成有效期为 expiry 的预签名下载地址。
func (s *Storage) SignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("ctyun: zos sign %s: %w", key, err)
	}
	return u.String(), nil
}
