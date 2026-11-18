package sysinfo

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/god-jason/goframe/errors"
	"github.com/god-jason/goframe/response"
)

// Register 按当前配置注册系统信息接口。未启用时不做任何注册。
func Register(engine *gin.Engine) {
	register(engine, GetConfig().normalize())
}

// register 使用显式配置注册，便于测试与自定义装配。
func register(engine *gin.Engine, cfg Config) {
	if engine == nil || !cfg.Enabled {
		return
	}
	path := normalizePath(cfg.Path)

	engine.GET(path, snapshotHandler(cfg))
	engine.GET(path+"/", snapshotHandler(cfg))

	group := engine.Group(path)
	group.GET("/cpu", cpuHandler(cfg))
	group.GET("/memory", memoryHandler(cfg))
	group.GET("/disk", diskHandler(cfg))
}

// snapshotHandler 返回完整快照。
func snapshotHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.Timeout)
		defer cancel()

		value, err := snapshot(ctx, cfg)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, value)
	}
}

// cpuHandler 返回 CPU 实时使用率。
func cpuHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.Timeout)
		defer cancel()

		value, err := cpuSnapshot(ctx, cfg)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, value)
	}
}

// memoryHandler 返回内存与交换分区使用情况。
func memoryHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.Timeout)
		defer cancel()

		value, err := memorySnapshot(ctx)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, value)
	}
}

// diskHandler 返回分区使用情况。
func diskHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.Timeout)
		defer cancel()

		value, err := diskSnapshot(ctx)
		if err != nil {
			writeError(c, err)
			return
		}
		response.Success(c, value)
	}
}

// writeError 把采集错误转换为统一响应，不向客户端暴露系统细节。
func writeError(c *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
		response.Fail(c, apperrors.Timeout())
		return
	}
	response.Fail(c, apperrors.Unavailable("system metrics unavailable").WithCause(err))
}

// normalizePath 补全前导斜杠并去掉尾部斜杠。
func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultConfig().Path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = DefaultConfig().Path
	}
	return path
}
