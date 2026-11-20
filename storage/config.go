package storage

// Config 是文件存储配置。
// local 驱动使用 Path/BaseURL，s3/minio 驱动使用 Endpoint/Bucket/Region/凭证。
type Config struct {
	Driver    string `yaml:"driver"`
	Path      string `yaml:"path"`
	BaseURL   string `yaml:"base_url"`
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	UseSSL    bool   `yaml:"use_ssl"`
	Secure    bool   `yaml:"secure"`
}

// DefaultConfig 返回存储默认配置。
func DefaultConfig() Config {
	return Config{
		Driver: "local",
		Path:   "data/storage",
	}
}
