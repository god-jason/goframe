package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ByteSize 表示字节数，支持在配置文件中使用 10MB、512KiB 等写法。
type ByteSize int64

// Bytes 返回字节数。
func (b ByteSize) Bytes() int64 { return int64(b) }

// String 返回可读的字节数表示。
func (b ByteSize) String() string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

var byteSizeUnits = []struct {
	suffix string
	factor float64
}{
	{"TIB", 1024 * 1024 * 1024 * 1024},
	{"TB", 1024 * 1024 * 1024 * 1024},
	{"GIB", 1024 * 1024 * 1024},
	{"GB", 1024 * 1024 * 1024},
	{"MIB", 1024 * 1024},
	{"MB", 1024 * 1024},
	{"KIB", 1024},
	{"KB", 1024},
	{"B", 1},
}

// ParseByteSize 解析带单位的字节数，例如 "10MB"、"512KiB"、"1024"。
func ParseByteSize(s string) (ByteSize, error) {
	raw := strings.ToUpper(strings.TrimSpace(s))
	if raw == "" {
		return 0, fmt.Errorf("empty byte size")
	}

	factor := float64(1)
	for _, u := range byteSizeUnits {
		if strings.HasSuffix(raw, u.suffix) {
			factor = u.factor
			raw = strings.TrimSpace(strings.TrimSuffix(raw, u.suffix))
			break
		}
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid byte size %q: must not be negative", s)
	}
	return ByteSize(value * factor), nil
}

// byteSizeDecodeHook 把 YAML 字符串形式的字节数转换为 ByteSize。
func byteSizeDecodeHook(from reflect.Type, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to != reflect.TypeOf(ByteSize(0)) {
		return data, nil
	}
	return ParseByteSize(data.(string))
}
