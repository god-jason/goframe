package app

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
)

type testModule struct {
	name  string
	deps  []string
	order *[]string
}

func (m *testModule) Name() string { return m.name }

func (m *testModule) Init(*App) error {
	*m.order = append(*m.order, m.name)
	return nil
}

func (m *testModule) RegisterRoutes(gin.IRouter) {}

func (m *testModule) Dependencies() []string { return m.deps }

func TestModuleDependencyOrder(t *testing.T) {
	var order []string
	manager := NewModuleManager()

	modules := []*testModule{
		{name: "c", deps: []string{"b"}, order: &order},
		{name: "a", order: &order},
		{name: "b", deps: []string{"a"}, order: &order},
	}
	for _, module := range modules {
		if err := manager.Register(module); err != nil {
			t.Fatalf("Register(%s): %v", module.name, err)
		}
	}

	if err := manager.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestModuleDependencyCycle(t *testing.T) {
	manager := NewModuleManager()
	var order []string
	if err := manager.Register(&testModule{name: "a", deps: []string{"b"}, order: &order}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(&testModule{name: "b", deps: []string{"a"}, order: &order}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Resolve(); err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestModuleMissingDependency(t *testing.T) {
	manager := NewModuleManager()
	var order []string
	if err := manager.Register(&testModule{name: "a", deps: []string{"missing"}, order: &order}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Resolve(); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestModuleDuplicateName(t *testing.T) {
	manager := NewModuleManager()
	var order []string
	if err := manager.Register(&testModule{name: "a", order: &order}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(&testModule{name: "a", order: &order}); err == nil {
		t.Fatal("expected duplicate module error")
	}
}

func TestLifecycleStartAndStopOrder(t *testing.T) {
	var called []string
	manager := NewModuleManager()
	for _, name := range []string{"a", "b"} {
		module := &testModule{name: name, order: &called}
		if err := manager.Register(&lifecycleModule{testModule: module, called: &called}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	want := []string{"start:a", "start:b", "stop:b", "stop:a"}
	if len(called) != len(want) {
		t.Fatalf("calls = %v, want %v", called, want)
	}
	for i := range want {
		if called[i] != want[i] {
			t.Fatalf("calls = %v, want %v", called, want)
		}
	}
}

type lifecycleModule struct {
	*testModule
	called *[]string
}

func (m *lifecycleModule) Start(context.Context) error {
	*m.called = append(*m.called, "start:"+m.name)
	return nil
}

func (m *lifecycleModule) Stop(context.Context) error {
	*m.called = append(*m.called, "stop:"+m.name)
	return nil
}
