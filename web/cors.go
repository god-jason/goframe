package web

import (
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// corsMiddleware 根据配置创建 CORS 中间件；未启用时返回 nil。
// 配置已在 Config.Validate 中校验，这里只负责把 "*" 转换为 AllowAllOrigins，
// 避免 cors 库因 AllowAllOrigins 与 AllowOrigins 同时存在而 panic。
func corsMiddleware(cfg CORSConfig) gin.HandlerFunc {
	if !cfg.Enabled {
		return nil
	}

	options := cors.Config{
		AllowMethods:     cfg.AllowedMethods,
		AllowHeaders:     cfg.AllowedHeaders,
		ExposeHeaders:    cfg.ExposedHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	}
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			options.AllowAllOrigins = true
			continue
		}
		if !options.AllowAllOrigins {
			options.AllowOrigins = append(options.AllowOrigins, origin)
		}
	}
	if options.AllowAllOrigins {
		options.AllowOrigins = nil
	}
	return cors.New(options)
}
