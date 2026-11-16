package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// Options 配置加载选项。
type Options struct {
	// File 显式指定配置文件路径，指定后文件必须存在。
	File string
	// EnvPrefix 环境变量前缀，默认 GOFRAME。
	EnvPrefix string
	// SearchPaths 未显式指定配置文件时的搜索目录。
	SearchPaths []string
}

// Option 配置加载选项函数。
type Option func(*Options)

// WithFile 指定配置文件路径。
func WithFile(path string) Option {
	return func(o *Options) { o.File = path }
}

// WithEnvPrefix 指定环境变量前缀。
func WithEnvPrefix(prefix string) Option {
	return func(o *Options) { o.EnvPrefix = prefix }
}

// WithSearchPaths 指定配置文件搜索目录。
func WithSearchPaths(paths ...string) Option {
	return func(o *Options) { o.SearchPaths = paths }
}

// Loader 负责加载、解析并校验配置。
// 加载顺序：默认值 -> 配置文件 -> 环境变量。
type Loader struct {
	opts Options
	v    *viper.Viper
	path string
}

// NewLoader 创建配置加载器。
func NewLoader(opts ...Option) *Loader {
	o := Options{EnvPrefix: "GOFRAME"}
	for _, opt := range opts {
		opt(&o)
	}
	if o.File == "" {
		o.File = os.Getenv("GOFRAME_CONFIG")
	}
	return &Loader{opts: o}
}

// Load 加载配置并完成校验。未找到配置文件时使用默认值。
func Load(opts ...Option) (*Config, error) {
	return NewLoader(opts...).Load()
}

var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv 展开配置文件中的 ${VAR} 和 ${VAR:-default} 占位符。
func expandEnv(content string) string {
	return envPlaceholder.ReplaceAllStringFunc(content, func(match string) string {
		groups := envPlaceholder.FindStringSubmatch(match)
		name := groups[1]
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		return groups[2]
	})
}

var configCandidates = []string{"config.yaml", "config.yml", "config.json", "config.toml"}

func (l *Loader) resolvePath() (string, error) {
	if l.opts.File != "" {
		info, err := os.Stat(l.opts.File)
		if err != nil {
			return "", fmt.Errorf("config: cannot access config file %s: %w", l.opts.File, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("config: %s is a directory, not a config file", l.opts.File)
		}
		return l.opts.File, nil
	}

	dirs := l.opts.SearchPaths
	if len(dirs) == 0 {
		dirs = []string{"./configs", "."}
	}
	for _, dir := range dirs {
		for _, name := range configCandidates {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", nil
}

// Load 执行配置加载。
func (l *Loader) Load() (*Config, error) {
	path, err := l.resolvePath()
	if err != nil {
		return nil, err
	}

	v := viper.New()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		expanded := expandEnv(string(raw))
		v.SetConfigFile(path)
		v.SetConfigType(strings.TrimPrefix(filepath.Ext(path), "."))
		if err := v.ReadConfig(bytes.NewBufferString(expanded)); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	v.SetEnvPrefix(l.opts.EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	bindEnv(v)

	cfg := Default()
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		byteSizeDecodeHook,
	))); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	l.v = v
	l.path = path
	return &cfg, nil
}

// Path 返回实际使用的配置文件路径，未使用文件时返回空字符串。
func (l *Loader) Path() string { return l.path }

// byteSizeDecodeHook 将字符串形式的字节数转换为 ByteSize。
func byteSizeDecodeHook(from reflect.Type, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to != reflect.TypeOf(ByteSize(0)) {
		return data, nil
	}
	return ParseByteSize(data.(string))
}

// bindEnv 显式绑定常用环境变量，Viper 的 AutomaticEnv 只对已注册的 Key 生效。
func bindEnv(v *viper.Viper) {
	bindings := map[string][]string{
		"app.environment":         {"GOFRAME_ENV", "APP_ENV"},
		"server.host":             {"GOFRAME_SERVER_HOST", "SERVER_HOST"},
		"server.port":             {"GOFRAME_SERVER_PORT", "SERVER_PORT"},
		"database.dsn":            {"GOFRAME_DATABASE_DSN", "DB_DSN"},
		"database.driver":         {"GOFRAME_DATABASE_DRIVER"},
		"redis.enabled":           {"GOFRAME_REDIS_ENABLED"},
		"redis.addr":              {"GOFRAME_REDIS_ADDR", "REDIS_ADDR"},
		"redis.password":          {"GOFRAME_REDIS_PASSWORD", "REDIS_PASSWORD"},
		"logger.level":            {"GOFRAME_LOG_LEVEL", "LOG_LEVEL"},
		"security.auth.secret":    {"GOFRAME_JWT_SECRET", "JWT_SECRET"},
		"security.encryption.key": {"GOFRAME_ENCRYPTION_KEY", "ENCRYPTION_KEY"},
	}
	for key, names := range bindings {
		_ = v.BindEnv(append([]string{key}, names...)...)
	}
}
