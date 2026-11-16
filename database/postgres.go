package database

import "gorm.io/driver/postgres"
import "gorm.io/gorm"

// postgresDialector 创建 PostgreSQL 方言。
func postgresDialector(dsn string) gorm.Dialector {
	return postgres.Open(dsn)
}
