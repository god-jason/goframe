// Package bootstrap 汇总导入框架的全部标准组件，触发各包 init 中的配置与 boot 任务注册。
//
// 业务 main 只需要：
//
//	import (
//	    "github.com/god-jason/goframe/boot"
//	    _ "github.com/god-jason/goframe/bootstrap"
//	)
//
// 然后 boot.Startup / boot.Shutdown 即可。需要精简依赖的项目可以不导入本包，
// 改为按需导入具体组件包。
package bootstrap

import (
	_ "github.com/god-jason/goframe/auth"
	_ "github.com/god-jason/goframe/broker"
	_ "github.com/god-jason/goframe/cache"
	_ "github.com/god-jason/goframe/database"
	_ "github.com/god-jason/goframe/event"
	_ "github.com/god-jason/goframe/event/outbox"
	_ "github.com/god-jason/goframe/license"
	_ "github.com/god-jason/goframe/logger"
	_ "github.com/god-jason/goframe/mqtt"
	_ "github.com/god-jason/goframe/permission"
	_ "github.com/god-jason/goframe/pool"
	_ "github.com/god-jason/goframe/scheduler"
	_ "github.com/god-jason/goframe/security/audit"
	_ "github.com/god-jason/goframe/security/crypto"
	_ "github.com/god-jason/goframe/security/csrf"
	_ "github.com/god-jason/goframe/security/ratelimit"
	_ "github.com/god-jason/goframe/storage"
	_ "github.com/god-jason/goframe/upload"
	_ "github.com/god-jason/goframe/web"
	_ "github.com/god-jason/goframe/websocket"
	_ "github.com/god-jason/goframe/weixin"
)
