package permission

// Policy 是 RBAC 角色权限策略。
type Policy struct {
	// Roles 是角色到权限列表的映射。
	Roles map[string][]string `json:"roles" yaml:"roles"`
	// SuperRoles 是拥有全部权限的角色。
	SuperRoles []string `json:"super_roles" yaml:"super_roles"`
}

// DefaultPolicy 返回默认策略：系统预置超级管理员角色。
func DefaultPolicy() Policy {
	return Policy{
		Roles:      make(map[string][]string),
		SuperRoles: []string{"super_admin", "system"},
	}
}
