// Package storage 定义统一文件存储接口。
package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/god-jason/goframe/config"
)

// Storage 是框架统一文件存储接口。文件 Key 由服务端生成，不使用客户端原始文件名。
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	GetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// New 根据配置创建存储实现。
func New(cfg config.StorageConfig) (Storage, error) {
	switch cfg.Driver {
	case "local":
		return NewLocal(cfg)
	case "s3", "minio":
		return nil, fmt.Errorf("storage: driver %q is not available in this build, use local", cfg.Driver)
	default:
		return nil, fmt.Errorf("storage: unsupported driver %q", cfg.Driver)
	}
}
