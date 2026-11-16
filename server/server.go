package server

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/logger"
)

// Server 是 HTTP 服务封装。底层使用标准库 http.Server，不使用 Gin 的 Run()。
type Server struct {
	cfg       config.ServerConfig
	log       logger.Logger
	http      *http.Server
	tlsConfig *tls.Config

	mu       sync.Mutex
	listener net.Listener
	errCh    chan error
	started  bool
}

// New 创建 HTTP 服务。
func New(opts Options) (*Server, error) {
	if opts.Handler == nil {
		return nil, fmt.Errorf("server: handler is nil")
	}
	log := opts.Logger
	if log == nil {
		log = logger.NewNop()
	}
	tlsConfig, err := buildTLSConfig(opts.TLS)
	if err != nil {
		return nil, err
	}

	httpServer := &http.Server{
		Addr:              opts.Config.Addr(),
		Handler:           opts.Handler,
		ReadTimeout:       opts.Config.ReadTimeout,
		ReadHeaderTimeout: opts.Config.ReadHeaderTimeout,
		WriteTimeout:      opts.Config.WriteTimeout,
		IdleTimeout:       opts.Config.IdleTimeout,
		MaxHeaderBytes:    opts.Config.MaxHeaderBytes,
		TLSConfig:         tlsConfig,
	}
	return &Server{
		cfg:       opts.Config,
		log:       log,
		http:      httpServer,
		tlsConfig: tlsConfig,
	}, nil
}

// Start 启动监听并在后台提供服务。
func (s *Server) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}

	listener, err := net.Listen("tcp", s.cfg.Addr())
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("server: listen %s: %w", s.cfg.Addr(), err)
	}
	if s.tlsConfig != nil {
		listener = tls.NewListener(listener, s.tlsConfig)
	}
	errCh := make(chan error, 1)
	s.listener = listener
	s.errCh = errCh
	s.started = true
	s.mu.Unlock()

	go func() {
		// Serve 运行在独立 goroutine，任何 panic 必须转换为错误回传，
		// 避免整个进程因单个连接处理异常而退出。
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("http server panic",
					logger.Any("panic", recovered),
					logger.String("stack", string(debug.Stack())),
				)
				errCh <- fmt.Errorf("server: serve panic: %v", recovered)
			}
		}()

		err := s.http.Serve(listener)
		if err != nil && !stderrors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	s.log.Info("http server started",
		logger.String("addr", listener.Addr().String()),
		logger.Bool("tls", s.tlsConfig != nil),
		logger.Duration("read_timeout", s.cfg.ReadTimeout),
		logger.Duration("write_timeout", s.cfg.WriteTimeout),
		logger.Duration("idle_timeout", s.cfg.IdleTimeout),
	)
	return nil
}

// Wait 等待服务退出并返回退出原因。
func (s *Server) Wait() error {
	s.mu.Lock()
	errCh := s.errCh
	s.mu.Unlock()
	if errCh == nil {
		return nil
	}
	return <-errCh
}

// Shutdown 优雅关闭服务，等待现有请求完成。
// 超时返回 ctx.Err()，由上层决定是否强制退出。
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return nil
	}
	s.log.Info("http server stopping")
	if err := s.http.Shutdown(ctx); err != nil {
		s.log.Warn("http server shutdown failed", logger.Error(err))
		return fmt.Errorf("server: shutdown: %w", err)
	}
	s.log.Info("http server stopped")
	return nil
}

// Addr 返回实际监听地址。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.cfg.Addr()
}

// RegisterOnShutdown 注册关闭前的回调。
func (s *Server) RegisterOnShutdown(fn func()) { s.http.RegisterOnShutdown(fn) }

// ShutdownTimeout 返回配置的关闭超时时间。
func (s *Server) ShutdownTimeout() time.Duration { return s.cfg.ShutdownTimeout }
