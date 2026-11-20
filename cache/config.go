package cache

// Config 是缓存配置：启用 Redis 时使用 Redis，否则使用进程内缓存。
type Config struct {
	Enabled   bool   `yaml:"enabled"`
	Addr      string `yaml:"addr"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	DB        int    `yaml:"db"`
	PoolSize  int    `yaml:"pool_size"`
	KeyPrefix string `yaml:"key_prefix"`
}

// DefaultConfig 返回缓存默认配置。
func DefaultConfig() Config {
	return Config{
		DB:        0,
		PoolSize:  20,
		KeyPrefix: "goframe:",
	}
}
