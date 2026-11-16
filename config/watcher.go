package config

import (
	"context"
	"fmt"
	"os"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Watch 监听配置文件变化并重新加载配置。
// 仅热更新明确支持动态调整的配置项（例如日志等级）；
// 监听地址、数据库连接等需要重建的配置不会被自动应用。
//
// 回调运行在 viper 的监听 goroutine 中，框架会捕获其 panic，
// 避免配置回调异常导致整个进程退出。
func (l *Loader) Watch(ctx context.Context, onChange func(*Config)) error {
	if l.path == "" {
		return fmt.Errorf("config: watching requires a config file")
	}

	watcher := viper.New()
	watcher.SetConfigFile(l.path)
	watcher.OnConfigChange(func(fsnotify.Event) {
		// 回调方可能是业务代码，任何 panic 都只能丢弃本次变更，不能终止进程。
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "config: watch callback panic: %v\n", recovered)
			}
		}()

		select {
		case <-ctx.Done():
			return
		default:
		}

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
	return nil
}
