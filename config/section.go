package config

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/mitchellh/mapstructure"
)

// Section 是一个配置段。每个组件在自己的包内声明配置结构体，并在 init 中注册：
//
//	var cfg = config.Register("logger", DefaultConfig())
//
//	func GetConfig() *Config { return cfg.Get() }
//
// 加载配置时会把 YAML 中同名节点解码到独立副本，再原子替换当前值，
// 因此热更新不会与读取方产生数据竞争。
type Section[T any] struct {
	name     string
	defaults T
	current  atomic.Pointer[T]
	keys     []string
}

// Register 注册配置段并返回句柄。名称不能为空，且不能重复注册。
// 这是 init 阶段的编程错误，非法调用会 panic 以便尽早发现。
func Register[T any](name string, defaults T) *Section[T] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("config: section name must not be empty")
	}
	section := &Section[T]{
		name:     name,
		defaults: defaults,
		keys:     collectKeys(name, reflect.TypeOf(defaults)),
	}
	initial := defaults
	section.current.Store(&initial)
	registerSection(section)
	return section
}

// Name 返回配置段名称。
func (s *Section[T]) Name() string { return s.name }

// Get 返回当前配置快照。返回值只读，不应被调用方修改。
func (s *Section[T]) Get() *T { return s.current.Load() }

// Defaults 返回注册时声明的默认值。
func (s *Section[T]) Defaults() T { return s.defaults }

func (s *Section[T]) envKeys() []string { return s.keys }

// load 从解析后的配置映射读取该配置段。缺失的字段保留默认值。
func (s *Section[T]) load(raw map[string]any) error {
	value := s.defaults
	if data, ok := lookupMap(raw, s.name); ok {
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:           &value,
			TagName:          "mapstructure",
			WeaklyTypedInput: true,
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				byteSizeDecodeHook,
			),
		})
		if err != nil {
			return fmt.Errorf("config: create decoder for section %q: %w", s.name, err)
		}
		if err := decoder.Decode(data); err != nil {
			return fmt.Errorf("config: decode section %q: %w", s.name, err)
		}
	}
	s.current.Store(&value)
	return nil
}

// section 是类型擦除后的配置段接口。
type section interface {
	Name() string
	envKeys() []string
	load(map[string]any) error
}

// lookupMap 按点分路径读取嵌套配置。
func lookupMap(raw map[string]any, path string) (any, bool) {
	var current any = raw
	for _, part := range strings.Split(path, ".") {
		values, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = values[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// collectKeys 递归收集结构体的叶子配置键，用于环境变量绑定。
func collectKeys(prefix string, t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return []string{prefix}
	}

	var keys []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("mapstructure")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		keys = append(keys, collectKeys(prefix+"."+name, field.Type)...)
	}
	if len(keys) == 0 {
		return []string{prefix}
	}
	return keys
}

// envName 把配置键转换为环境变量名：logger.rotation.max_size -> GOFRAME_LOGGER_ROTATION_MAX_SIZE。
func envName(key string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	return "GOFRAME_" + strings.ToUpper(replacer.Replace(key))
}
