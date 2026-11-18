package i18n

import (
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// LocaleKey 是语言在 Gin Context 中的键。
	LocaleKey = "goframe.locale"

	// DefaultHeaderName 是默认的语言请求头。
	DefaultHeaderName = "X-Locale"
	// DefaultQueryName 是默认的语言查询参数。
	DefaultQueryName = "lang"
	// DefaultCookieName 是默认的语言 Cookie 名称。
	DefaultCookieName = "locale"
)

// MiddlewareConfig 是语言协商中间件配置。
type MiddlewareConfig struct {
	HeaderName string
	QueryName  string
	CookieName string
}

// DefaultMiddlewareConfig 返回默认的语言协商中间件配置。
func DefaultMiddlewareConfig() MiddlewareConfig {
	return MiddlewareConfig{
		HeaderName: DefaultHeaderName,
		QueryName:  DefaultQueryName,
		CookieName: DefaultCookieName,
	}
}

func (c MiddlewareConfig) withDefaults() MiddlewareConfig {
	if c.HeaderName == "" {
		c.HeaderName = DefaultHeaderName
	}
	if c.QueryName == "" {
		c.QueryName = DefaultQueryName
	}
	if c.CookieName == "" {
		c.CookieName = DefaultCookieName
	}
	return c
}

// Middleware 返回使用默认配置的语言协商中间件。
func (b *Bundle) Middleware() gin.HandlerFunc {
	return b.MiddlewareWithConfig(DefaultMiddlewareConfig())
}

// MiddlewareWithConfig 返回语言协商中间件。
// 检测顺序为 X-Locale、?lang=、locale Cookie、Accept-Language；未匹配到已注册语言时使用默认语言。
// 语言会写入 Gin Context（LocaleKey）与请求 context（WithLocale）。
func (b *Bundle) MiddlewareWithConfig(cfg MiddlewareConfig) gin.HandlerFunc {
	cfg = cfg.withDefaults()
	return func(c *gin.Context) {
		locale := b.detectLocale(c, cfg)
		if locale == "" {
			locale = b.DefaultLocale()
		}
		if locale != "" {
			c.Set(LocaleKey, locale)
			c.Request = c.Request.WithContext(WithLocale(c.Request.Context(), locale))
		}
		c.Next()
	}
}

// LocaleFromGin 从 Gin Context 读取语言，回退到请求 context。
func LocaleFromGin(c *gin.Context) string {
	if value, ok := c.Get(LocaleKey); ok {
		if locale, ok := value.(string); ok {
			return locale
		}
	}
	if c.Request == nil {
		return ""
	}
	return Locale(c.Request.Context())
}

func (b *Bundle) detectLocale(c *gin.Context, cfg MiddlewareConfig) string {
	if locale, ok := b.Match(c.GetHeader(cfg.HeaderName)); ok {
		return locale
	}
	if locale, ok := b.Match(c.Query(cfg.QueryName)); ok {
		return locale
	}
	if locale, err := c.Cookie(cfg.CookieName); err == nil {
		if matched, ok := b.Match(locale); ok {
			return matched
		}
	}
	for _, locale := range ParseAcceptLanguage(c.GetHeader("Accept-Language")) {
		if matched, ok := b.Match(locale); ok {
			return matched
		}
	}
	return ""
}

// ParseAcceptLanguage 解析 Accept-Language 请求头，按 q 值从高到低返回语言标签。
// 通配符 * 与 q=0 的条目会被忽略，相同 q 值保持原始顺序。
func ParseAcceptLanguage(header string) []string {
	type entry struct {
		tag string
		q   float64
	}

	entries := make([]entry, 0, 4)
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}

		q := 1.0
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if len(param) < 2 || !strings.EqualFold(param[:2], "q=") {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
			if err != nil {
				continue
			}
			if value > 1 {
				value = 1
			}
			q = value
		}
		if q <= 0 {
			continue
		}
		entries = append(entries, entry{tag: tag, q: q})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].q > entries[j].q
	})

	locales := make([]string, 0, len(entries))
	for _, item := range entries {
		locales = append(locales, item.tag)
	}
	return locales
}
