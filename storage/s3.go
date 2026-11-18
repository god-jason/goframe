package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// defaultPresignTTL 是未指定有效期时预签名 URL 的默认时长。
const defaultPresignTTL = time.Hour

// s3API 是 S3Storage 依赖的最小客户端接口，便于测试注入替身。
type s3API interface {
	PutObject(ctx context.Context, bucket, object string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	GetObject(ctx context.Context, bucket, object string, opts minio.GetObjectOptions) (io.ReadCloser, error)
	RemoveObject(ctx context.Context, bucket, object string, opts minio.RemoveObjectOptions) error
	StatObject(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	PresignedGetObject(ctx context.Context, bucket, object string, expires time.Duration, reqParams url.Values) (*url.URL, error)
}

// minioAPI 把 *minio.Client 适配为 s3API。GetObject 以 io.ReadCloser 返回，便于替身实现。
type minioAPI struct {
	client *minio.Client
}

func (m minioAPI) PutObject(ctx context.Context, bucket, object string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	return m.client.PutObject(ctx, bucket, object, reader, objectSize, opts)
}

func (m minioAPI) GetObject(ctx context.Context, bucket, object string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	return m.client.GetObject(ctx, bucket, object, opts)
}

func (m minioAPI) RemoveObject(ctx context.Context, bucket, object string, opts minio.RemoveObjectOptions) error {
	return m.client.RemoveObject(ctx, bucket, object, opts)
}

func (m minioAPI) StatObject(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return m.client.StatObject(ctx, bucket, object, opts)
}

func (m minioAPI) PresignedGetObject(ctx context.Context, bucket, object string, expires time.Duration, reqParams url.Values) (*url.URL, error) {
	return m.client.PresignedGetObject(ctx, bucket, object, expires, reqParams)
}

// S3Storage 是兼容 S3 协议的对象存储实现，可用于 MinIO、AWS S3 等。
type S3Storage struct {
	client s3API
	bucket string
}

var _ Storage = (*S3Storage)(nil)

// NewS3 创建 S3/MinIO 存储。构造过程只校验配置并初始化客户端，不发起网络请求。
func NewS3(cfg Config) (Storage, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("storage: s3 endpoint is required")
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("storage: s3 bucket is required")
	}
	accessKey := strings.TrimSpace(cfg.AccessKey)
	secretKey := strings.TrimSpace(cfg.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("storage: s3 access_key and secret_key are required")
	}

	secure := cfg.UseSSL || cfg.Secure
	// 允许 endpoint 携带协议头，此时以协议头为准，避免与 secure 冲突。
	switch {
	case strings.HasPrefix(strings.ToLower(endpoint), "https://"):
		endpoint = endpoint[len("https://"):]
		secure = true
	case strings.HasPrefix(strings.ToLower(endpoint), "http://"):
		endpoint = endpoint[len("http://"):]
		secure = false
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("storage: s3 endpoint is invalid")
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: strings.TrimSpace(cfg.Region),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: create s3 client: %w", err)
	}
	return &S3Storage{client: minioAPI{client: client}, bucket: bucket}, nil
}

// Put 上传对象。大小未知时传 -1，由 minio-go 自动选择分片上传。
func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("storage: reader is nil")
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return err
	}
	if _, err := s.client.PutObject(ctx, s.bucket, clean, r, -1, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	return nil
}

// Get 读取对象，返回的 ReadCloser 由调用方关闭。
func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return nil, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, clean, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	return object, nil
}

// Delete 删除对象，对象不存在时返回 nil。
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, clean, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

// Exists 判断对象是否存在。
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return false, err
	}
	if _, err := s.client.StatObject(ctx, s.bucket, clean, minio.StatObjectOptions{}); err != nil {
		response := minio.ToErrorResponse(err)
		if response.StatusCode == http.StatusNotFound || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound" {
			return false, nil
		}
		return false, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	return true, nil
}

// GetURL 返回对象的预签名地址，ttl 小于等于 0 时使用 1 小时。
func (s *S3Storage) GetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = defaultPresignTTL
	}
	url, err := s.client.PresignedGetObject(ctx, s.bucket, clean, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %q: %w", key, err)
	}
	return url.String(), nil
}
