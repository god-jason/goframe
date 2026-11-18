package outbox

import (
	"context"

	"github.com/god-jason/goframe/boot"
	"github.com/god-jason/goframe/database"
	"github.com/god-jason/goframe/event"
)

var global = struct {
	relay *Relay
}{}

// databaseGet 与 eventGet 以变量形式暴露，便于测试注入依赖。
var (
	databaseGet = database.Get
	eventGet    = event.Get
)

func init() {
	_ = boot.Register("outbox", &boot.Module{
		Startup:  Startup,
		Shutdown: Shutdown,
		Depends:  []string{"config", "logger", "database", "event"},
	})
}

// Get 返回全局 Outbox 投递器；未启用时返回 nil。
func Get() *Relay {
	return global.relay
}

// Startup 自动迁移 outbox 表并启动后台投递。
func Startup(ctx context.Context) error {
	current := GetConfig()
	if !current.Enabled {
		return nil
	}
	db := databaseGet()
	if db == nil {
		return nil
	}
	relay := New(db.DB(), eventGet(), current.Options())
	if err := relay.AutoMigrate(); err != nil {
		return err
	}
	if err := relay.Start(ctx); err != nil {
		return err
	}
	global.relay = relay
	return nil
}

// Shutdown 停止后台投递。
func Shutdown(ctx context.Context) error {
	relay := global.relay
	global.relay = nil
	if relay == nil {
		return nil
	}
	return relay.Stop(ctx)
}
