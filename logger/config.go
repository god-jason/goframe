package logger

// RotationConfig 是日志滚动配置，单独定义在 logger 包内。
type RotationConfig struct {
	// Enabled 是否开启滚动；默认开启。
	Enabled bool `yaml:"enabled"`
	// MaxSize 是单个日志文件上限。
	MaxSize int64 `yaml:"max_size"`
	// MaxAge 是旧日志保留天数。
	MaxAge int `yaml:"max_age"`
	// MaxBackups 是旧日志保留数量。
	MaxBackups int `yaml:"max_backups"`
	// Compress 是否压缩旧日志。
	Compress bool `yaml:"compress"`
	// LocalTime 备份时间是否使用本地时区。
	LocalTime bool `yaml:"local_time"`
}

// Config 是 logger 包配置。
type Config struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	// Output 可以是 stdout / stderr / 文件路径；为空时写入 logs/app.log。
	Output   string         `yaml:"output"`
	Rotation RotationConfig `yaml:"rotation"`
}

// DefaultConfig 返回 logger 默认配置。
func DefaultConfig() Config {
	return Config{
		Level:  "info",
		Format: "json",
		Output: "logs/app.log",
		Rotation: RotationConfig{
			Enabled:    true,
			MaxSize:    100 << 20,
			MaxAge:     30,
			MaxBackups: 10,
			Compress:   true,
			LocalTime:  true,
		},
	}
}

// optionsFromConfig 把包配置转换为底层 Logger 选项。
func optionsFromConfig(cfg Config) Options {
	return Options{
		Level:  cfg.Level,
		Format: cfg.Format,
		Output: cfg.Output,
		Rotate: RotateOptions{
			Disabled:   !cfg.Rotation.Enabled,
			MaxSize:    cfg.Rotation.MaxSize,
			MaxAge:     cfg.Rotation.MaxAge,
			MaxBackups: cfg.Rotation.MaxBackups,
			Compress:   cfg.Rotation.Compress,
			LocalTime:  cfg.Rotation.LocalTime,
		},
	}
}
