package goroku

import (
	"context"
	"goroku/goroku/inline"
	"testing"
)

type mockCallingModule struct {
	im *inline.InlineManager
}

func (m *mockCallingModule) Call() string {
	return inline.DetectCallingModuleForTest(m.im)
}

func (m *mockCallingModule) Name() string { return "Tester" }

func (m *mockCallingModule) Store(unit *inline.Unit) {
	m.im.StoreUnit("renamed", unit)
}

type mockInlineModuleRegistry struct {
	module any
}

func (m *mockInlineModuleRegistry) ModuleNames() []string { return []string{"Tester"} }
func (m *mockInlineModuleRegistry) WithModule(name string, fn func(any)) bool {
	if name != "Tester" {
		return false
	}
	fn(m.module)
	return true
}

func TestDetectCallingModule(t *testing.T) {
	caller := &mockCallingModule{}
	registry := &mockInlineModuleRegistry{module: caller}
	im := inline.NewInlineManager(nil, nil, registry)
	defer im.Close(context.Background())
	caller.im = im
	got := caller.Call()
	expected := "Tester"
	if got != expected {
		t.Errorf("detectCallingModule failed: expected %q, got %q", expected, got)
	}
}

func TestStoreUnitUsesCanonicalModuleName(t *testing.T) {
	caller := &mockCallingModule{}
	registry := &mockInlineModuleRegistry{module: caller}
	im := inline.NewInlineManager(nil, nil, registry)
	defer im.Close(context.Background())
	caller.im = im
	unit := &inline.Unit{}

	caller.Store(unit)

	if unit.Module != "Tester" {
		t.Fatalf("expected canonical module name Tester, got %q", unit.Module)
	}
}
