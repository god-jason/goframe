package health

import "context"

// DatabaseCheck 创建数据库健康检查。
func DatabaseCheck(name string, db interface {
	Ping(ctx context.Context) error
}) Check {
	return Check{
		Name:     name,
		Critical: true,
		Fn:       db.Ping,
	}
}

// CacheCheck 创建缓存健康检查。
func CacheCheck(name string, pinger interface {
	Ping(ctx context.Context) error
}) Check {
	return Check{
		Name:     name,
		Critical: true,
		Fn:       pinger.Ping,
	}
}

// FuncCheck 创建自定义健康检查。
func FuncCheck(name string, critical bool, fn func(ctx context.Context) error) Check {
	return Check{Name: name, Critical: critical, Fn: fn}
}
