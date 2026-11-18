// Package id 提供框架统一的标识生成能力。
package id

import "github.com/google/uuid"

// newV7 与 newRandom 以变量形式暴露，便于测试覆盖生成失败时的回退分支。
var (
	newV7     = uuid.NewV7
	newRandom = uuid.New
)

// New 生成 UUIDv7 字符串，按时间有序，便于作为数据库主键。
func New() string {
	if v, err := newV7(); err == nil {
		return v.String()
	}
	return newRandom().String()
}

// NewUUID 生成 UUIDv7。
func NewUUID() uuid.UUID {
	v, err := newV7()
	if err != nil {
		return newRandom()
	}
	return v
}

// Parse 解析字符串形式的 UUID。
func Parse(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// IsValid 判断字符串是否为合法 UUID。
func IsValid(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
