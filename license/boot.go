package license

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"strings"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/logger"
)

var global = struct {
	manager *Manager
}{}

func init() {
	_ = boot.Register("license", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger"},
	})
}

// Get 返回全局许可管理器；未启用或加载失败时返回 nil。
func Get() *Manager {
	return global.manager
}

// Startup 加载并校验许可。Strict 为 true 时，任何加载错误都会阻断启动。
func Startup(ctx context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	if strings.TrimSpace(current.File) == "" {
		return fmt.Errorf("license: file is required when enabled")
	}

	publicKey, err := loadPublicKey(current.PublicKey)
	if err != nil {
		return err
	}
	revocation, err := loadRevocation(current.RevocationFile, publicKey)
	if err != nil {
		return err
	}

	fingerprint := strings.TrimSpace(current.Fingerprint)
	fingerprintSource := "config"
	if fingerprint == "" {
		fingerprintSource = ""
		info, err := CollectFingerprint(ctx, FingerprintOptions{Sources: current.FingerprintSources})
		if err != nil {
			logger.Get().Warn("license fingerprint unavailable", logger.Error(err))
		} else {
			fingerprint = info.Fingerprint
			fingerprintSource = info.Source
			logger.Get().Debug("license fingerprint resolved", logger.String("source", info.Source))
		}
	}

	manager, err := New(Options{
		PublicKey:      publicKey,
		LicenseFile:    current.File,
		Fingerprint:    fingerprint,
		Revocation:     revocation,
		StateFile:      current.StateFile,
		ClockTolerance: current.ClockTolerance,
	})
	if err != nil {
		if current.Strict {
			return err
		}
		logger.Get().Warn("license invalid, continue in unlicensed mode", logger.Error(err))
		return nil
	}

	global.manager = manager

	snapshot := manager.Snapshot()
	fields := []logger.Field{
		logger.String("status", string(snapshot.Status)),
		logger.Bool("valid", snapshot.Valid),
	}
	if fingerprintSource != "" {
		fields = append(fields, logger.String("fingerprint_source", fingerprintSource))
	}
	if snapshot.License != nil {
		fields = append(fields,
			logger.String("license_id", snapshot.License.ID),
			logger.String("edition", snapshot.License.Edition),
		)
	}
	if snapshot.Reason != "" {
		fields = append(fields, logger.String("reason", snapshot.Reason))
	}
	if snapshot.Valid {
		logger.Get().Info("license loaded", fields...)
	} else {
		logger.Get().Warn("license not valid", fields...)
	}
	return nil
}

// Shutdown 释放许可管理器。
func Shutdown(context.Context) error {
	global.manager = nil
	return nil
}

// loadPublicKey 支持内联 PEM 或文件路径。
func loadPublicKey(value string) (ed25519.PublicKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("license: public_key is required")
	}
	data := []byte(value)
	if !strings.Contains(value, "BEGIN PUBLIC KEY") {
		content, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("license: read public key: %w", err)
		}
		data = content
	}
	return ParsePublicKeyPEM(data)
}

// loadRevocation 读取并校验吊销列表。
func loadRevocation(path string, publicKey ed25519.PublicKey) (*RevocationList, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("license: read revocation list: %w", err)
	}
	list, err := VerifyRevocation(content, publicKey)
	if err != nil {
		return nil, err
	}
	return list, nil
}
