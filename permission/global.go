package permission

import "sync"

var global = struct {
	sync.RWMutex
	authorizer Authorizer
}{authorizer: NewRBAC(DefaultPolicy())}

// Get 返回全局授权器。默认使用空策略 + 超级管理员角色。
func Get() Authorizer {
	global.RLock()
	defer global.RUnlock()
	return global.authorizer
}

// Set 替换全局授权器；传入 nil 时恢复默认策略。
func Set(authorizer Authorizer) {
	if authorizer == nil {
		authorizer = NewRBAC(DefaultPolicy())
	}
	global.Lock()
	global.authorizer = authorizer
	global.Unlock()
}
