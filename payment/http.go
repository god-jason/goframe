package payment

import (
	"bytes"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/god-jason/goframe/api"
)

// Register 按当前配置把回调路由登记为框架根路径接口（POST {notify_path}/wechat、
// POST {notify_path}/alipay），由 web 启动时统一挂载。未启用时不做任何登记。
func Register() {
	register(GetConfig().normalize())
}

// register 使用显式配置登记，便于测试与自定义装配。
func register(cfg Config) {
	if !cfg.Enabled {
		return
	}
	base := resolvePath(cfg.NotifyPath)
	api.RegisterRoot(http.MethodPost, base+"/wechat", notifyHandler(ChannelWechat))
	api.RegisterRoot(http.MethodPost, base+"/alipay", notifyHandler(ChannelAlipay))
}

// resolvePath 规范化路径，空值或根路径回退到默认前缀。
func resolvePath(path string) string {
	resolved := api.NormalizePath(path)
	if resolved == "/" {
		resolved = api.NormalizePath(DefaultConfig().NotifyPath)
	}
	return resolved
}

// notifyHandler 返回渠道回调处理器：还原原始 body（验签需要）→ 验签与分发 → 渠道格式应答。
func notifyHandler(name ChannelName) gin.HandlerFunc {
	return func(c *gin.Context) {
		manager := Get()
		if manager == nil {
			ackNotify(c, name, http.StatusServiceUnavailable)
			return
		}

		// SDK 验签要求读取原始请求体，先取出再还原。
		if body, err := c.GetRawData(); err == nil {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}

		if _, err := manager.HandleNotify(c.Request.Context(), name, c.Request); err != nil {
			ackNotify(c, name, http.StatusInternalServerError)
			return
		}
		ackNotify(c, name, http.StatusOK)
	}
}

// ackNotify 按渠道约定应答：微信成功返回 200 空 body、失败返回 500 JSON；
// 支付宝成功返回文本 success、失败返回 failure。
func ackNotify(c *gin.Context, name ChannelName, status int) {
	if name == ChannelAlipay {
		if status == http.StatusOK {
			c.String(http.StatusOK, "success")
			return
		}
		c.String(http.StatusInternalServerError, "failure")
		return
	}
	if status == http.StatusOK {
		c.Status(http.StatusOK)
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"code": "FAIL", "message": "payment notify process failed"})
}
