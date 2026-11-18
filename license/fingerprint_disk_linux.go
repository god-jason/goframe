//go:build linux

package license

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// diskSerial 读取系统磁盘序列号，用于机器指纹。
func diskSerial(ctx context.Context) string {
	matches, err := filepath.Glob("/sys/block/*/serial")
	if err != nil {
		return ""
	}

	serials := make([]string, 0, len(matches))
	for _, path := range matches {
		if ctx != nil && ctx.Err() != nil {
			return ""
		}
		device := filepath.Base(filepath.Dir(path))
		if strings.HasPrefix(device, "loop") || strings.HasPrefix(device, "ram") ||
			strings.HasPrefix(device, "dm-") || strings.HasPrefix(device, "sr") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			serials = append(serials, value)
		}
	}
	sort.Strings(serials)
	return strings.Join(serials, ",")
}
