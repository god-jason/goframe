package config

import (
	"os"
	"regexp"
)

var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv 展开配置文件中的 ${VAR} 和 ${VAR:-default} 占位符。
func expandEnv(content string) string {
	return envPlaceholder.ReplaceAllStringFunc(content, func(match string) string {
		groups := envPlaceholder.FindStringSubmatch(match)
		name := groups[1]
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		return groups[2]
	})
}
