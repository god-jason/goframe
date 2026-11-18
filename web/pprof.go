package web

import "github.com/god-jason/goframe/config"

// PProfConfig 是 pprof 路由配置。
type PProfConfig struct {
	// Enabled 控制是否在 Web 引擎上注册 pprof 路由。生产环境默认关闭。
	Enabled bool `mapstructure:"enabled"`
	// Path 是 pprof 路由前缀。
	Path string `mapstructure:"path"`
}

// DefaultPProfConfig 返回 pprof 默认配置。
func DefaultPProfConfig() PProfConfig {
	return PProfConfig{
		Enabled: false,
		Path:    "/debug/pprof",
	}
}

var pprofCfg = config.Register("pprof", DefaultPProfConfig())

// PProf 返回当前 pprof 配置快照。
func PProf() *PProfConfig { return pprofCfg.Get() }
