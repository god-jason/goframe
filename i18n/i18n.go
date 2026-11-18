// Package i18n 提供多语言文案管理与请求语言协商能力。
package i18n

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// localeContextKey 是语言标签在 context 中的键。
type localeContextKey struct{}

// Bundle 是线程安全的多语言文案集合。
// 语言标签大小写不敏感，内部统一规范化为小写并以 - 分隔（如 zh-CN）。
type Bundle struct {
	mu            sync.RWMutex
	defaultLocale string
	messages      map[string]map[string]string
}

// New 创建 Bundle。defaultLocale 为空时使用 en。
func New(defaultLocale string) *Bundle {
	locale := normalizeLocale(defaultLocale)
	if locale == "" {
		locale = "en"
	}
	return &Bundle{
		defaultLocale: locale,
		messages:      make(map[string]map[string]string),
	}
}

// Add 追加或覆盖指定语言的文案。
// 同一语言多次调用会合并文案，空语言或空 key 会被忽略。
func (b *Bundle) Add(locale string, messages map[string]string) {
	loc := normalizeLocale(locale)
	if loc == "" || len(messages) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.messages[loc]
	if !ok {
		entry = make(map[string]string, len(messages))
		b.messages[loc] = entry
	}
	for key, value := range messages {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		entry[key] = value
	}
}

// Translate 按语言标签翻译 key。
// 回退链为：精确语言 -> 上级语言标签（zh-Hant-TW -> zh-Hant -> zh）-> 默认语言 -> key 本身。
// 提供 args 时会使用 fmt.Sprintf 格式化文案。
func (b *Bundle) Translate(locale, key string, args ...any) string {
	if key == "" {
		return ""
	}
	template, ok := b.lookup(locale, key)
	if !ok {
		return key
	}
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

// DefaultLocale 返回默认语言。
func (b *Bundle) DefaultLocale() string {
	return b.defaultLocale
}

// Locales 返回已注册的语言标签，按字典序排列。
func (b *Bundle) Locales() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sortedLocalesLocked()
}

// Match 将请求语言标签匹配到已注册语言。
// 支持精确匹配、上级标签匹配，以及请求仅含语言主标签时匹配已注册的区域变体。
func (b *Bundle) Match(locale string) (string, bool) {
	loc := normalizeLocale(locale)
	if loc == "" {
		return "", false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if matched, ok := b.matchLocked(loc); ok {
		return matched, true
	}
	if !strings.Contains(loc, "-") {
		for _, registered := range b.sortedLocalesLocked() {
			if strings.HasPrefix(registered, loc+"-") {
				return registered, true
			}
		}
	}
	return "", false
}

// lookup 按回退链查找文案。
func (b *Bundle) lookup(locale, key string) (string, bool) {
	loc := normalizeLocale(locale)

	b.mu.RLock()
	defer b.mu.RUnlock()

	if value, ok := b.lookupChainLocked(loc, key); ok {
		return value, true
	}
	// 请求只给出语言主标签时，依次尝试该语言下已注册的区域变体。
	if loc != "" && !strings.Contains(loc, "-") {
		for _, registered := range b.sortedLocalesLocked() {
			if !strings.HasPrefix(registered, loc+"-") {
				continue
			}
			if value, ok := b.messages[registered][key]; ok {
				return value, true
			}
		}
	}
	if value, ok := b.lookupChainLocked(b.defaultLocale, key); ok {
		return value, true
	}
	return "", false
}

func (b *Bundle) lookupChainLocked(locale, key string) (string, bool) {
	for current := locale; current != ""; current = parentLocale(current) {
		messages, ok := b.messages[current]
		if !ok {
			continue
		}
		if value, ok := messages[key]; ok {
			return value, true
		}
	}
	return "", false
}

func (b *Bundle) matchLocked(locale string) (string, bool) {
	for current := locale; current != ""; current = parentLocale(current) {
		if _, ok := b.messages[current]; ok {
			return current, true
		}
	}
	return "", false
}

func (b *Bundle) sortedLocalesLocked() []string {
	locales := make([]string, 0, len(b.messages))
	for locale := range b.messages {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	return locales
}

// WithLocale 将语言标签写入 context，标签会被规范化。
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeContextKey{}, normalizeLocale(locale))
}

// Locale 从 context 读取语言标签，不存在时返回空字符串。
func Locale(ctx context.Context) string {
	locale, _ := ctx.Value(localeContextKey{}).(string)
	return locale
}

// normalizeLocale 将语言标签规范化为小写、以 - 分隔的形式。
func normalizeLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	locale = strings.ReplaceAll(locale, "_", "-")
	return strings.ToLower(locale)
}

// parentLocale 返回上一级语言标签，已是主标签时返回空字符串。
func parentLocale(locale string) string {
	index := strings.LastIndexByte(locale, '-')
	if index <= 0 {
		return ""
	}
	return locale[:index]
}
