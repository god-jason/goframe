package server

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/god-jason/goframe/logger"
)

const minTLSVersion = tls.VersionTLS12

// tlsMaterial 是 TLS 运行所需的材料。
// file 模式只有 config；acme 模式可能额外需要 HTTP-01 挑战服务器。
type tlsMaterial struct {
	config        *tls.Config
	challenge     *http.Server
	challengeAddr string
	requireHTTP   bool
}

// buildTLSConfig 根据配置构造 TLS 配置。
//
// none：由反向代理终止 TLS。
// file：加载已有证书和私钥。
// acme：使用 autocert 自动申请和续期证书，支持 HTTP-01 与 TLS-ALPN-01。
// DNS-01 需要额外的 DNS Provider 集成，当前未实现。
func buildTLSConfig(cfg TLSConfig, log logger.Logger) (*tlsMaterial, error) {
	if log == nil {
		log = logger.NewNop()
	}
	switch cfg.Mode {
	case "", "none":
		return &tlsMaterial{}, nil

	case "file":
		certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("server: load certificate: %w", err)
		}
		return &tlsMaterial{config: &tls.Config{
			MinVersion:   minTLSVersion,
			Certificates: []tls.Certificate{certificate},
		}}, nil

	case "acme":
		if len(cfg.ACME.Domains) == 0 {
			return nil, fmt.Errorf("server: tls.acme.domains must not be empty")
		}
		if strings.TrimSpace(cfg.ACME.Email) == "" {
			return nil, fmt.Errorf("server: tls.acme.email must not be empty")
		}
		switch cfg.ACME.Challenge {
		case "", "http-01", "tls-alpn-01":
		case "dns-01":
			return nil, fmt.Errorf("server: tls.acme.challenge dns-01 is not supported yet")
		default:
			return nil, fmt.Errorf("server: unsupported acme challenge %q", cfg.ACME.Challenge)
		}

		cacheDir := cfg.ACME.CacheDir
		if cacheDir == "" {
			cacheDir = "data/acme"
		}
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("server: create acme cache dir: %w", err)
		}

		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(cacheDir),
			Email:      cfg.ACME.Email,
			HostPolicy: autocert.HostWhitelist(cfg.ACME.Domains...),
		}
		if cfg.ACME.RenewBefore > 0 {
			manager.RenewBefore = cfg.ACME.RenewBefore
		}
		if cfg.ACME.DirectoryURL != "" {
			manager.Client = &acme.Client{DirectoryURL: cfg.ACME.DirectoryURL}
		}

		tlsConfig := manager.TLSConfig()
		tlsConfig.MinVersion = minTLSVersion

		material := &tlsMaterial{config: tlsConfig}
		// HTTP-01 需要独立的 :80 监听；TLS-ALPN-01 由 TLS 握手自动完成。
		if cfg.ACME.Challenge != "tls-alpn-01" && !cfg.ACME.DisableHTTP {
			addr := cfg.ACME.HTTPChallengeAddr
			if addr == "" {
				addr = ":80"
			}
			material.challengeAddr = addr
			material.requireHTTP = cfg.ACME.Challenge == "http-01"
			material.challenge = &http.Server{
				Addr:              addr,
				Handler:           manager.HTTPHandler(http.NotFoundHandler()),
				ReadHeaderTimeout: 10 * time.Second,
			}
			log.Info("acme http challenge configured", logger.String("addr", addr))
		}
		return material, nil

	default:
		return nil, fmt.Errorf("server: unsupported tls mode %q", cfg.Mode)
	}
}
