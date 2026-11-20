package api

import "github.com/god-jason/goframe/config"

// Options 是路由分组参数。
type Options struct {
	APIPrefix  string
	APIVersion string
}

// SPAConfig 是前端静态资源配置。
type SPAConfig struct {
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"`
	Path    string `yaml:"path"`
	Index   string `yaml:"index"`
}

// DefaultSPAConfig 返回 SPA 默认配置。
func DefaultSPAConfig() SPAConfig {
	return SPAConfig{Mode: "embed", Index: "index.html"}
}

var spaCfg = config.Register("spa", DefaultSPAConfig())

// SPA 返回当前 SPA 配置快照。
func SPA() *SPAConfig { return spaCfg.Get() }
