package tencent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// Storage 是腾讯云 COS 对象存储客户端（绑定默认存储桶）。
type Storage struct {
	client *cos.Client
	cfg    Config
}

// NewStorage 按配置创建 COS 客户端。
func NewStorage(cfg Config) (*Storage, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.bucketURL())
	if err != nil {
		return nil, fmt.Errorf("tencent: parse cos bucket url: %w", err)
	}
	client := cos.NewClient(&cos.BaseURL{BucketURL: u}, &http.Client{
		Timeout: cfg.Timeout,
		Transport: &cos.AuthorizationTransport{
			SecretID:  cfg.SecretID,
			SecretKey: cfg.SecretKey,
		},
	})
	return &Storage{client: client, cfg: cfg}, nil
}

// Client 返回底层 COS 客户端，便于使用完整 SDK 能力。
func (s *Storage) Client() *cos.Client { return s.client }

// Put 上传对象。
func (s *Storage) Put(ctx context.Context, key string, body io.Reader) error {
	if _, err := s.client.Object.Put(ctx, key, body, nil); err != nil {
		return fmt.Errorf("tencent: cos put %s: %w", key, err)
	}
	return nil
}

// Get 下载对象，调用方负责关闭返回的 ReadCloser。
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.client.Object.Get(ctx, key, nil)
	if err != nil {
		return nil, fmt.Errorf("tencent: cos get %s: %w", key, err)
	}
	return resp.Body, nil
}

// Delete 删除对象。
func (s *Storage) Delete(ctx context.Context, key string) error {
	if _, err := s.client.Object.Delete(ctx, key, nil); err != nil {
		return fmt.Errorf("tencent: cos delete %s: %w", key, err)
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
	u, err := s.client.Object.GetPresignedURL2(ctx, http.MethodGet, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("tencent: cos sign url %s: %w", key, err)
	}
	return u.String(), nil
}
