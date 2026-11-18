package storage

// Config 是文件存储配置。
// local 驱动使用 Path/BaseURL，s3/minio 驱动使用 Endpoint/Bucket/Region/凭证。
type Config struct {
	Driver    string `mapstructure:"driver"`
	Path      string `mapstructure:"path"`
	BaseURL   string `mapstructure:"base_url"`
	Endpoint  string `mapstructure:"endpoint"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl"`
	Secure    bool   `mapstructure:"secure"`
}

// DefaultConfig 返回存储默认配置。
func DefaultConfig() Config {
	return Config{
		Driver: "local",
		Path:   "data/storage",
	}
}
