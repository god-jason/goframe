package database

import (
	"context"

	"gorm.io/gorm"
)

type txContextKey struct{}

// WithTx 将事务对象放入 context，供多个 Repository 共享同一事务。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFrom 从 context 中取出事务对象。
func TxFrom(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txContextKey{}).(*gorm.DB)
	return tx, ok
}

// Conn 返回当前请求应使用的数据库连接：存在事务时返回事务对象，
// 否则返回基础连接。Repository 实现应统一通过该方法获取连接。
func Conn(ctx context.Context, base *gorm.DB) *gorm.DB {
	if tx, ok := TxFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	return base.WithContext(ctx)
}
