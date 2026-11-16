// Package sanitize 提供富文本与纯文本安全处理。
package sanitize

import "github.com/microcosm-cc/bluemonday"

var (
	richTextPolicy = bluemonday.UGCPolicy()
	plainPolicy    = bluemonday.StrictPolicy()
)

// RichText 清理富文本，保留安全的排版标签。
func RichText(input string) string {
	return richTextPolicy.Sanitize(input)
}

// PlainText 移除全部 HTML 标签。
func PlainText(input string) string {
	return plainPolicy.Sanitize(input)
}

// StripTags 是 PlainText 的别名，便于语义化调用。
func StripTags(input string) string {
	return PlainText(input)
}
