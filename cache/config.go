package cache

// Config 是缓存配置：启用 Redis 时使用 Redis，否则使用进程内缓存。
type Config struct {
	Enabled   bool   `mapstructure:"enabled"`
	Addr      string `mapstructure:"addr"`
	Username  string `mapstructure:"username"`
	Password  string `mapstructure:"password"`
	DB        int    `mapstructure:"db"`
	PoolSize  int    `mapstructure:"pool_size"`
	KeyPrefix string `mapstructure:"key_prefix"`
}

// DefaultConfig 返回缓存默认配置。
func DefaultConfig() Config {
	return Config{
		DB:        0,
		PoolSize:  20,
		KeyPrefix: "goframe:",
	}
}
