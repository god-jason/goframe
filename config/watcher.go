package config

import (
	"context"
	"fmt"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Watch 监听配置文件变化并重新加载配置。
// 仅热更新明确支持动态调整的配置项（例如日志等级）。
func (l *Loader) Watch(ctx context.Context, onChange func(*Config)) error {
	if l.path == "" {
		return fmt.Errorf("config: watching requires a config file")
	}

	watcher := viper.New()
	watcher.SetConfigFile(l.path)
	watcher.OnConfigChange(func(fsnotify.Event) {
		cfg, err := NewLoader(
			WithFile(l.path),
			WithEnvPrefix(l.opts.EnvPrefix),
			WithSearchPaths(l.opts.SearchPaths...),
		).Load()
		if err != nil {
			return
		}
		if onChange != nil {
			onChange(cfg)
		}
	})
	watcher.WatchConfig()

	go func() {
		<-ctx.Done()
		watcher.OnConfigChange(nil)
	}()
	return nil
}
