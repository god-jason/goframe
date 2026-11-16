package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/gin-gonic/gin"
)

// Module 是业务模块接口。框架通过模块化装配业务能力。
type Module interface {
	// Name 返回模块唯一名称。
	Name() string
	// Init 在路由注册前完成模块初始化。
	Init(app *App) error
	// RegisterRoutes 只注册本模块路由，不允许修改全局中间件。
	RegisterRoutes(router gin.IRouter)
}

// Dependency 是模块可选依赖接口，用于声明对其他模块的依赖。
type Dependency interface {
	Dependencies() []string
}

// ModuleManager 负责模块注册、依赖检查、初始化顺序与生命周期管理。
type ModuleManager struct {
	mu       sync.RWMutex
	modules  map[string]Module
	order    []string
	resolved []Module
	started  []Lifecycle
}

// NewModuleManager 创建模块管理器。
func NewModuleManager() *ModuleManager {
	return &ModuleManager{modules: make(map[string]Module)}
}

// Register 注册模块，检查名称为空与重复。
func (m *ModuleManager) Register(module Module) error {
	if module == nil {
		return fmt.Errorf("app: module is nil")
	}
	name := module.Name()
	if name == "" {
		return fmt.Errorf("app: module name must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resolved != nil {
		return fmt.Errorf("app: cannot register module %q after modules resolved", name)
	}
	if _, exists := m.modules[name]; exists {
		return fmt.Errorf("app: module %q already registered", name)
	}
	m.modules[name] = module
	m.order = append(m.order, name)
	return nil
}

// Get 返回指定模块。
func (m *ModuleManager) Get(name string) (Module, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	module, ok := m.modules[name]
	return module, ok
}

// List 按注册顺序返回模块。
func (m *ModuleManager) List() []Module {
	m.mu.RLock()
	defer m.mu.RUnlock()
	modules := make([]Module, 0, len(m.order))
	for _, name := range m.order {
		modules = append(modules, m.modules[name])
	}
	return modules
}

// Resolve 检查依赖是否存在、是否存在循环依赖，并返回拓扑排序后的模块列表。
func (m *ModuleManager) Resolve() ([]Module, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resolved != nil {
		return append([]Module(nil), m.resolved...), nil
	}

	indegree := make(map[string]int, len(m.order))
	edges := make(map[string][]string, len(m.order))
	for _, name := range m.order {
		indegree[name] = 0
	}
	for _, name := range m.order {
		deps := dependenciesOf(m.modules[name])
		for _, dep := range deps {
			if dep == name {
				return nil, fmt.Errorf("app: module %q depends on itself", name)
			}
			if _, ok := m.modules[dep]; !ok {
				return nil, fmt.Errorf("app: module %q depends on unknown module %q", name, dep)
			}
			edges[dep] = append(edges[dep], name)
			indegree[name]++
		}
	}

	var queue []string
	for _, name := range m.order {
		if indegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var resolved []Module
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		resolved = append(resolved, m.modules[name])
		for _, next := range edges[name] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(resolved) != len(m.order) {
		var remaining []string
		for _, name := range m.order {
			if indegree[name] > 0 {
				remaining = append(remaining, name)
			}
		}
		return nil, fmt.Errorf("app: module dependency cycle detected: %v", remaining)
	}

	m.resolved = resolved
	return append([]Module(nil), resolved...), nil
}

func dependenciesOf(module Module) []string {
	if dep, ok := module.(Dependency); ok {
		return dep.Dependencies()
	}
	return nil
}

// Init 按依赖顺序初始化模块。
func (m *ModuleManager) Init(app *App) error {
	resolved, err := m.Resolve()
	if err != nil {
		return err
	}
	for _, module := range resolved {
		name := module.Name()
		if err := runSafely(func() error { return module.Init(app) }); err != nil {
			return fmt.Errorf("app: init module %q: %w", name, err)
		}
	}
	return nil
}

// RegisterRoutes 按依赖顺序注册模块路由。
func (m *ModuleManager) RegisterRoutes(router gin.IRouter) error {
	resolved, err := m.Resolve()
	if err != nil {
		return err
	}
	for _, module := range resolved {
		name := module.Name()
		if err := runSafely(func() error {
			module.RegisterRoutes(router)
			return nil
		}); err != nil {
			return fmt.Errorf("app: register routes for module %q: %w", name, err)
		}
	}
	return nil
}

// Start 按依赖顺序启动实现了 Lifecycle 的模块。
func (m *ModuleManager) Start(ctx context.Context) error {
	resolved, err := m.Resolve()
	if err != nil {
		return err
	}
	for _, module := range resolved {
		lifecycle, ok := module.(Lifecycle)
		if !ok {
			continue
		}
		name := module.Name()
		if err := runSafely(func() error { return lifecycle.Start(ctx) }); err != nil {
			return fmt.Errorf("app: start module %q: %w", name, err)
		}
		m.mu.Lock()
		m.started = append(m.started, lifecycle)
		m.mu.Unlock()
	}
	return nil
}

// Stop 逆序停止已启动的模块。
func (m *ModuleManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	started := append([]Lifecycle(nil), m.started...)
	m.started = nil
	m.mu.Unlock()

	var firstErr error
	for i := len(started) - 1; i >= 0; i-- {
		lifecycle := started[i]
		if err := runSafely(func() error { return lifecycle.Stop(ctx) }); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func runSafely(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return fn()
}
