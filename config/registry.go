package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/god-jason/goframe/boot"
)

// AppConfig 是应用元信息，属于 config 包自身配置。
type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
}

// DefaultAppConfig 返回应用元信息默认值。
func DefaultAppConfig() AppConfig {
	return AppConfig{
		Name:        "goframe-app",
		Version:     "dev",
		Environment: "development",
	}
}

var (
	appSection *Section[AppConfig]

	registry = struct {
		mu        sync.RWMutex
		sections  []section
		file      string
		callbacks []func()
		cancel    context.CancelFunc
	}{}
)

func init() {
	appSection = Register("app", DefaultAppConfig())
	// config 是最底层任务，其他组件都通过 Depends: []string{"config"} 排在其后。
	_ = boot.Register("config", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
	})
}

// App 返回应用元信息配置。
func App() *AppConfig { return appSection.Get() }

func registerSection(s section) {
	registry.mu.Lock()
	registry.sections = append(registry.sections, s)
	registry.mu.Unlock()
}

// SetFile 显式指定配置文件路径。必须在 boot.Startup 前调用。
func SetFile(path string) {
	registry.mu.Lock()
	registry.file = path
	registry.mu.Unlock()
}

// File 返回当前配置文件路径，未配置时为空。
func File() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.file
}

// OnReload 注册配置重载成功后的回调。
// 回调应避免阻塞；库内部会隔离 panic。
func OnReload(fn func()) {
	if fn == nil {
		return
	}
	registry.mu.Lock()
	registry.callbacks = append(registry.callbacks, fn)
	registry.mu.Unlock()
}

// Startup 是 config 的 boot 入口：加载配置文件并启动监听。
// 文件不存在时使用各组件注册的默认值。
func Startup(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	path, explicit := resolveSectionFile()
	if err := loadSections(path, explicit); err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	watchCtx, cancel := context.WithCancel(ctx)
	registry.mu.Lock()
	registry.cancel = cancel
	registry.mu.Unlock()
	go watchSections(watchCtx, path)
	return nil
}

// Shutdown 停止配置监听。
func Shutdown(context.Context) error {
	registry.mu.Lock()
	cancel := registry.cancel
	registry.cancel = nil
	registry.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Reload 重新读取配置文件并更新所有已注册配置段。
func Reload() error {
	path, explicit := resolveSectionFile()
	return loadSections(path, explicit)
}

// loadSections 读取文件并解码到每个配置段。
func loadSections(path string, explicit bool) error {
	raw := map[string]any{}
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			if explicit {
				return fmt.Errorf("config: read %s: %w", path, err)
			}
		} else {
			expanded := expandEnv(string(content))
			if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
				return fmt.Errorf("config: parse %s: %w", path, err)
			}
			if raw == nil {
				raw = map[string]any{}
			}
			raw = lowerKeys(raw).(map[string]any)
		}
	}

	registry.mu.RLock()
	sections := append([]section(nil), registry.sections...)
	registry.mu.RUnlock()
	// 环境变量按 GOFRAME_<SECTION>_<KEY> 覆盖文件值，包括文件中不存在的键。
	for _, item := range sections {
		for _, key := range item.envKeys() {
			if value, ok := os.LookupEnv(envName(key)); ok {
				setPath(raw, key, value)
			}
		}
	}

	for _, item := range sections {
		if err := item.load(raw); err != nil {
			return err
		}
	}

	notifyReload()
	return nil
}

// setPath 把环境变量值写入嵌套配置映射。
func setPath(raw map[string]any, path, value string) {
	parts := strings.Split(path, ".")
	current := raw
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

// notifyReload 调用配置重载回调，隔离业务回调 panic。
func notifyReload() {
	registry.mu.RLock()
	callbacks := append([]func(){}, registry.callbacks...)
	registry.mu.RUnlock()

	for _, callback := range callbacks {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					fmt.Fprintf(os.Stderr, "config: reload callback panic: %v\n", recovered)
				}
			}()
			callback()
		}()
	}
}

// resolveSectionFile 查找配置文件。显式指定的文件（绝对或相对路径）必须存在；
// 未显式指定时依次在当前工作目录、可执行文件所在目录下查找默认配置文件。
func resolveSectionFile() (string, bool) {
	registry.mu.RLock()
	file := registry.file
	registry.mu.RUnlock()
	if file == "" {
		file = os.Getenv("GOFRAME_CONFIG")
	}
	if file != "" {
		return file, true
	}

	bases := []string{"."}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "." {
			bases = append(bases, dir)
		}
	}
	names := []string{
		filepath.Join("configs", "config.yaml"),
		filepath.Join("configs", "config.yml"),
		"config.yaml",
		"config.yml",
	}
	for _, base := range bases {
		for _, name := range names {
			candidate := filepath.Join(base, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, false
			}
		}
	}
	return "", false
}

// lowerKeys 递归把配置映射的键统一为小写，保持与原 viper 行为一致，
// 使环境变量与配置段的点分路径查找不受大小写影响。
func lowerKeys(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[strings.ToLower(key)] = lowerKeys(item)
		}
		return out
	case []any:
		for i, item := range v {
			v[i] = lowerKeys(item)
		}
	}
	return value
}

// watchSections 监听配置文件变化并重载，ctx 取消后退出。
func watchSections(ctx context.Context, path string) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()
	if err := watcher.Add(filepath.Dir(absolute)); err != nil {
		return
	}

	var (
		timer  *time.Timer
		timerC <-chan time.Time
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != absolute {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(200 * time.Millisecond)
			} else {
				timer.Reset(200 * time.Millisecond)
			}
			timerC = timer.C
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "config: watch error: %v\n", err)
		case <-timerC:
			timerC = nil
			if err := Reload(); err != nil {
				fmt.Fprintf(os.Stderr, "config: reload failed: %v\n", err)
			}
		}
	}
}
