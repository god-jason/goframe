//go:build darwin

package license

import (
	"context"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// diskSerial 读取系统磁盘序列号，用于机器指纹。
func diskSerial(ctx context.Context) string {
	value, err := disk.SerialNumberWithContext(ctx, "")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
