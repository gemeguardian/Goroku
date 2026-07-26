//go:build linux

package modules

import (
	"goroku/goroku"
	"testing"
)

func TestFreshHotModuleInstanceUsesRememberedFactory(t *testing.T) {
	old := &directRollbackModule{name: "FreshRollback"}
	rememberHotModuleFactory(old, func() goroku.Module {
		return &directRollbackModule{name: "FreshRollback"}
	})
	fresh, err := freshHotModuleInstance(old)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("rollback reused the unloaded module instance")
	}
}
