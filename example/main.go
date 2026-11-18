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
	"errors"
	"flag"
	"fmt"
	"io"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 启动示例服务并阻塞，直到 ctx 取消或服务异常退出，返回进程退出码。
func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runContext(ctx, args, stdout, stderr)
}

// runContext 与 run 相同，但允许调用方注入可取消的 context 便于测试。
func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("example", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFile := flags.String("config", "", "配置文件路径，默认自动搜索 configs/config.yaml")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *configFile != "" {
		config.SetFile(*configFile)
	}

	if err := boot.Startup(ctx); err != nil {
		fmt.Fprintf(stderr, "startup: %v\n", err)
		return 1
	}

	// 业务模块在基础设施就绪后手动初始化，并把路由注册到 web.API()。
	module := demo.New()
	if err := module.Init(); err != nil {
		fmt.Fprintf(stderr, "initialize module: %v\n", err)
		shutdown(stderr)
		return 1
	}
	module.RegisterRoutes(web.API())

	// HTTP 服务最后启动，阻塞到收到退出信号或服务异常。
	if err := web.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "run web: %v\n", err)
	}

	shutdown(stderr)
	return 0
}

// shutdown 逆序释放所有 boot 资源。
func shutdown(stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := boot.Shutdown(ctx); err != nil {
		fmt.Fprintf(stderr, "shutdown: %v\n", err)
	}
}
