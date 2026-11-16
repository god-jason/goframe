package app

import "context"

// Lifecycle 是可选的生命周期接口。实现该接口的模块会在 HTTP 服务启动前启动，
// 在 HTTP 服务停止后逆序停止。
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
