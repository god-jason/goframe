package server

import (
	"crypto/tls"
	"fmt"

	"github.com/god-jason/goframe/config"
)

const minTLSVersion = tls.VersionTLS12

// buildTLSConfig 根据配置构造 TLS 配置。
// none 表示由反向代理终止 TLS；file 加载已有证书；acme 需要外部证书管理组件。
func buildTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	switch cfg.Mode {
	case "", "none":
		return nil, nil
	case "file":
		certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("server: load certificate: %w", err)
		}
		return &tls.Config{
			MinVersion:   minTLSVersion,
			Certificates: []tls.Certificate{certificate},
		}, nil
	case "acme":
		return nil, fmt.Errorf("server: tls.mode acme is not available in this build, use file mode or terminate TLS at the reverse proxy")
	default:
		return nil, fmt.Errorf("server: unsupported tls mode %q", cfg.Mode)
	}
}
