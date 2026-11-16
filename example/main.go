// Command example 演示框架的标准启动方式。
//
// 运行：go run ./example
// 默认监听 :8080，使用内存会话与内存缓存；未配置数据库时不连接数据库。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/god-jason/goframe/app"
	"github.com/god-jason/goframe/example/modules/demo"
)

func main() {
	application, err := app.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize application: %v\n", err)
		os.Exit(1)
	}

	if err := application.Register(demo.New()); err != nil {
		fmt.Fprintf(os.Stderr, "register module: %v\n", err)
		os.Exit(1)
	}

	if err := application.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "run application: %v\n", err)
		os.Exit(1)
	}
}
