package response

import "strings"

const (
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = 20
	// MaxPageSize 允许的最大每页条数。
	MaxPageSize = 100
)

// PageQuery 是统一分页与排序参数。
type PageQuery struct {
	Page     int    `form:"page" json:"page"`
	PageSize int    `form:"page_size" json:"page_size"`
	Sort     string `form:"sort" json:"sort"`
	Order    string `form:"order" json:"order"`
}

// Normalize 规范化分页参数：补齐默认值并限制最大值。
func (q *PageQuery) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = DefaultPageSize
	}
	if q.PageSize > MaxPageSize {
		q.PageSize = MaxPageSize
	}
	q.Order = strings.ToLower(strings.TrimSpace(q.Order))
	if q.Order != "asc" && q.Order != "desc" {
		q.Order = "desc"
	}
	q.Sort = strings.TrimSpace(q.Sort)
}

// Offset 返回数据库查询偏移量。
func (q PageQuery) Offset() int {
	return (q.Page - 1) * q.PageSize
}

// SortClause 根据白名单将用户排序参数映射为数据库字段。
// allowed 的 key 是允许的 API 字段名，value 是对应的数据库列名。
// 未命中白名单时回退到 fallback，绝不把用户输入直接拼接进 SQL。
func (q PageQuery) SortClause(allowed map[string]string, fallback string) (column string, direction string) {
	column = allowed[q.Sort]
	if column == "" {
		column = allowed[fallback]
	}
	if column == "" {
		column = fallback
	}
	direction = q.Order
	if direction != "asc" && direction != "desc" {
		direction = "desc"
	}
	return column, direction
}
