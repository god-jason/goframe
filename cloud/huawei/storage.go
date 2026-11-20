package huawei

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
)

// Storage 是华为云 OBS 对象存储客户端（绑定默认桶）。
type Storage struct {
	client *obs.ObsClient
	cfg    Config
}

// NewStorage 按配置创建 OBS 客户端。
func NewStorage(cfg Config) (*Storage, error) {
	cfg = cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	timeoutSec := int(cfg.Timeout / time.Second)
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	client, err := obs.New(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Storage.Endpoint,
		obs.WithConnectTimeout(timeoutSec), obs.WithSocketTimeout(timeoutSec))
	if err != nil {
		return nil, fmt.Errorf("huawei: create obs client: %w", err)
	}
	return &Storage{client: client, cfg: cfg}, nil
}

// Client 返回底层 OBS 客户端，便于使用完整 SDK 能力。
func (s *Storage) Client() *obs.ObsClient { return s.client }

// Put 上传对象。
func (s *Storage) Put(ctx context.Context, key string, body io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.PutObject(&obs.PutObjectInput{
		PutObjectBasicInput: obs.PutObjectBasicInput{
			ObjectOperationInput: obs.ObjectOperationInput{
				Bucket: s.cfg.Storage.Bucket,
				Key:    key,
			},
		},
		Body: body,
	})
	if err != nil {
		return fmt.Errorf("huawei: obs put %s: %w", key, err)
	}
	return nil
}

// Get 下载对象，调用方负责关闭返回的 ReadCloser。
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := s.client.GetObject(&obs.GetObjectInput{
		GetObjectMetadataInput: obs.GetObjectMetadataInput{
			Bucket: s.cfg.Storage.Bucket,
			Key:    key,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("huawei: obs get %s: %w", key, err)
	}
	return output.Body, nil
}

// Delete 删除对象。
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(&obs.DeleteObjectInput{
		Bucket: s.cfg.Storage.Bucket,
		Key:    key,
	})
	if err != nil {
		return fmt.Errorf("huawei: obs delete %s: %w", key, err)
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
	output, err := s.client.CreateSignedUrl(&obs.CreateSignedUrlInput{
		Method:  obs.HttpMethodGet,
		Bucket:  s.cfg.Storage.Bucket,
		Key:     key,
		Expires: int(expiry / time.Second),
	})
	if err != nil {
		return "", fmt.Errorf("huawei: obs sign url %s: %w", key, err)
	}
	return output.SignedUrl, nil
}

// Close 释放 OBS 客户端连接。
func (s *Storage) Close() { s.client.Close() }
