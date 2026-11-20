package upload

import "github.com/god-jason/goframe/config"

// Config 是文件上传配置。
type Config struct {
	MaxSize           config.ByteSize `yaml:"max_size"`
	ScanEnabled       bool            `yaml:"scan_enabled"`
	ClamAV            ClamAVConfig    `yaml:"clamav"`
	AllowedExtensions []string        `yaml:"allowed_extensions"`
	AllowedMIMETypes  []string        `yaml:"allowed_mime_types"`
	ImageMaxWidth     int             `yaml:"image_max_width"`
	ImageMaxHeight    int             `yaml:"image_max_height"`
	ImageMaxPixels    int64           `yaml:"image_max_pixels"`
	ImageQuality      int             `yaml:"image_quality"`
}

// DefaultConfig 返回上传默认配置。
func DefaultConfig() Config {
	return Config{
		MaxSize:           50 << 20,
		ClamAV:            DefaultClamAVConfig(),
		AllowedExtensions: []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".txt", ".csv", ".zip"},
		ImageMaxWidth:     4096,
		ImageMaxHeight:    4096,
		ImageMaxPixels:    32 << 20,
		ImageQuality:      85,
	}
}
