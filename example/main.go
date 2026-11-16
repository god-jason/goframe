// Command example 演示 boot 方式的标准启动流程。
//
// 启动顺序：
//  1. main 设置配置路径与可选注入（WebSocket 鉴权、Broker 鉴权等）；
//  2. boot.Startup 按依赖拓扑启动 config、logger、数据库、缓存、MQTT、Broker、Web 引擎等；
//  3. 业务模块手动初始化并注册路由；
//  4. web.Run 最后启动 HTTP 服务；
//  5. 收到退出信号后 boot.Shutdown 逆序释放资源。
//
// 运行：go run ./example
// 默认监听 :8080，使用内存会话与内存缓存；未配置数据库时不连接数据库。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/god-jason/goframe/boot"
	_ "github.com/god-jason/goframe/bootstrap"
	"github.com/god-jason/goframe/config"
	"github.com/god-jason/goframe/example/modules/demo"
	"github.com/god-jason/goframe/web"
)

func main() {
	configFile := flag.String("config", "", "配置文件路径，默认自动搜索 configs/config.yaml")
	flag.Parse()

	if *configFile != "" {
		config.SetFile(*configFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := boot.Startup(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "startup: %v\n", err)
		os.Exit(1)
	}

	// 业务模块在基础设施就绪后手动初始化，并把路由注册到 web.API()。
	module := demo.New()
	if err := module.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "initialize module: %v\n", err)
		shutdown()
		os.Exit(1)
	}
	module.RegisterRoutes(web.API())

	// HTTP 服务最后启动，阻塞到收到退出信号或服务异常。
	if err := web.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "run web: %v\n", err)
	}

	shutdown()
}

// shutdown 逆序释放所有 boot 资源。
func shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := boot.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}
