//go:build !linux && !darwin && !windows

package license

import "context"

// diskSerial 在不支持的平台返回空值。
func diskSerial(context.Context) string { return "" }
