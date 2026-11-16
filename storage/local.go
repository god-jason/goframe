package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/god-jason/goframe/config"
)

// LocalStorage 是基于本地目录的存储实现。文件必须存放在 Web Root 之外。
type LocalStorage struct {
	root    string
	baseURL string
}

// NewLocal 创建本地存储。
func NewLocal(cfg config.StorageConfig) (*LocalStorage, error) {
	root, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve path %q: %w", cfg.Path, err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create root %q: %w", root, err)
	}
	return &LocalStorage{root: root, baseURL: strings.TrimRight(cfg.BaseURL, "/")}, nil
}

// sanitizeKey 校验并规范化文件 Key，防止路径穿越。
func sanitizeKey(key string) (string, error) {
	if strings.ContainsAny(key, "\x00\\") {
		return "", fmt.Errorf("storage: invalid key %q", key)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return "", fmt.Errorf("storage: invalid key %q", key)
		}
	}
	clean := path.Clean("/" + strings.TrimSpace(key))
	if clean == "/" {
		return "", fmt.Errorf("storage: empty key")
	}
	return strings.TrimPrefix(clean, "/"), nil
}

func (s *LocalStorage) resolve(key string) (string, string, error) {
	clean, err := sanitizeKey(key)
	if err != nil {
		return "", "", err
	}
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	// 二次校验，确保拼接后的路径仍在根目录内。
	if rel, err := filepath.Rel(s.root, full); err != nil || strings.HasPrefix(rel, "..") {
		return "", "", fmt.Errorf("storage: key %q escapes storage root", key)
	}
	return clean, full, nil
}

// Put 写入文件。写入过程使用临时文件 + Rename，避免产生半截文件。
func (s *LocalStorage) Put(ctx context.Context, key string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return fmt.Errorf("storage: create directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return fmt.Errorf("storage: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: write file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("storage: commit file: %w", err)
	}
	return nil
}

// Get 打开文件。
func (s *LocalStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, full, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", key, err)
	}
	return file, nil
}

// Delete 删除文件，文件不存在时返回 nil。
func (s *LocalStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: delete %q: %w", key, err)
	}
	return nil
}

// Exists 判断文件是否存在。
func (s *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, full, err := s.resolve(key)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}

// GetURL 返回文件访问地址。私有文件应由上层提供短期签名 URL。
func (s *LocalStorage) GetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	clean, err := sanitizeKey(key)
	if err != nil {
		return "", err
	}
	if s.baseURL != "" {
		return s.baseURL + "/" + clean, nil
	}
	return "/" + clean, nil
}
