// Package tlscfg 提供各组件（web、mqtt、broker 等）复用的 TLS 配置构造，
// 统一最低 TLS 版本与证书加载逻辑。
package tlscfg

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// MinVersion 是框架统一要求的最低 TLS 版本。
const MinVersion = tls.VersionTLS12

// Server 加载服务端证书与私钥，返回最低 TLS 1.2 的 TLS 配置。
func Server(certFile, keyFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tlscfg: load certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   MinVersion,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

// Client 构造客户端 TLS 配置：可选根 CA、可选客户端证书、可选 ServerName。
func Client(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: MinVersion,
		ServerName: serverName,
	}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("tlscfg: read ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("tlscfg: invalid ca file")
		}
		cfg.RootCAs = pool
	}
	if certFile != "" || keyFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("tlscfg: load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{certificate}
	}
	return cfg, nil
}
