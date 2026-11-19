package sysinfo

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
	apperrors "github.com/god-jason/goframe/errors"
)

// Register 按当前配置把系统信息接口登记为框架根路径接口，由 web 启动时统一挂载。
// 未启用时不做任何登记。
func Register() {
	register(GetConfig().normalize())
}

// register 使用显式配置登记，便于测试与自定义装配。
func register(cfg Config) {
	if !cfg.Enabled {
		return
	}
	path := resolvePath(cfg.Path)

	api.RegisterRoot(http.MethodGet, path, collectHandler(cfg, func(ctx context.Context) (any, error) {
		return snapshot(ctx, cfg)
	}))
	api.RegisterRoot(http.MethodGet, path+"/", collectHandler(cfg, func(ctx context.Context) (any, error) {
		return snapshot(ctx, cfg)
	}))
	api.RegisterRoot(http.MethodGet, path+"/cpu", collectHandler(cfg, func(ctx context.Context) (any, error) {
		return cpuSnapshot(ctx, cfg)
	}))
	api.RegisterRoot(http.MethodGet, path+"/memory", collectHandler(cfg, func(ctx context.Context) (any, error) {
		return memorySnapshot(ctx)
	}))
	api.RegisterRoot(http.MethodGet, path+"/disk", collectHandler(cfg, func(ctx context.Context) (any, error) {
		return diskSnapshot(ctx)
	}))
}

// collectHandler 把采集函数统一包装为带超时与标准响应的处理器。
func collectHandler(cfg Config, collect func(context.Context) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.Timeout)
		defer cancel()

		value, err := collect(ctx)
		if err != nil {
			writeError(c, err)
			return
		}
		api.Success(c, value)
	}
}

// writeError 把采集错误转换为统一响应，不向客户端暴露系统细节。
func writeError(c *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
		api.Fail(c, apperrors.Timeout())
		return
	}
	api.Fail(c, apperrors.Unavailable("system metrics unavailable").WithCause(err))
}

// resolvePath 规范化路径，空值或根路径回退到默认路径。
func resolvePath(path string) string {
	resolved := api.NormalizePath(path)
	if resolved == "/" {
		resolved = api.NormalizePath(DefaultConfig().Path)
	}
	return resolved
}
