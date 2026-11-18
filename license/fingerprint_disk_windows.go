//go:build windows

package license

import (
	"context"
	"sort"
	"strings"
)

// diskSerial 读取系统磁盘序列号，用于机器指纹。
func diskSerial(ctx context.Context) string {
	output := commandOutput(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_DiskDrive | Select-Object -ExpandProperty SerialNumber")
	if output == "" {
		output = commandOutput(ctx, "wmic", "diskdrive", "get", "serialnumber")
	}

	serials := make([]string, 0, 4)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "SerialNumber") {
			continue
		}
		serials = append(serials, line)
	}
	sort.Strings(serials)
	return strings.Join(serials, ",")
}
