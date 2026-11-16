package database

import "gorm.io/driver/mysql"
import "gorm.io/gorm"

// mysqlDialector 创建 MySQL 方言。
func mysqlDialector(dsn string) gorm.Dialector {
	return mysql.Open(dsn)
}
