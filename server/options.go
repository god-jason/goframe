// Package server 封装基于标准库 http.Server 的 HTTP 服务。
package server

import (
	"net/http"

	"github.com/god-jason/goframe/logger"
)

// Options 是 HTTP 服务构造选项。
type Options struct {
	Config  Config
	TLS     TLSConfig
	Handler http.Handler
	Logger  logger.Logger
}
