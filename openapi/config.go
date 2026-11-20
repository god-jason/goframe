// Package openapi 提供 Swagger UI 文档页面注册。
//
// 业务需要先通过 swag 生成文档并在启动入口匿名导入生成的 docs 包：
//
//	swag init -g cmd/server/main.go -o docs
//
//	import _ "example/docs"
//
// 生成后 openapi.Register 才能提供 /swagger/doc.json 等文档接口。
package openapi

import (
	"github.com/god-jason/goframe/config"
)

// Config 是 OpenAPI 文档配置。
type Config struct {
	// Enabled 控制是否注册文档路由。生产环境通常保持关闭或仅在内部网络开启。
	Enabled bool `yaml:"enabled"`
	// Path 是 Swagger UI 的挂载路径。
	Path string `yaml:"path"`
}

// DefaultConfig 返回 OpenAPI 文档默认配置。
func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Path:    "/swagger",
	}
}

var cfg = config.Register("openapi", DefaultConfig())

// GetConfig 返回当前 OpenAPI 配置快照。
func GetConfig() *Config { return cfg.Get() }
