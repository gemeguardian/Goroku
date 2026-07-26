package modules

import (
	"fmt"
	"goroku/goroku"
	"reflect"
	"sync"
)

var hotModuleFactories sync.Map

func hotModuleInstanceKey(mod goroku.Module) (uintptr, bool) {
	if mod == nil {
		return 0, false
	}
	value := reflect.ValueOf(mod)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return 0, false
	}
	return value.Pointer(), true
}

func rememberHotModuleFactory(mod goroku.Module, factory func() goroku.Module) {
	if key, ok := hotModuleInstanceKey(mod); ok && factory != nil {
		hotModuleFactories.Store(key, factory)
	}
}

func freshHotModuleInstance(mod goroku.Module) (goroku.Module, error) {
	key, ok := hotModuleInstanceKey(mod)
	if !ok {
		return mod, nil
	}
	value, ok := hotModuleFactories.Load(key)
	if !ok {
		return mod, nil
	}
	factory := value.(func() goroku.Module)
	fresh := factory()
	if fresh == nil {
		return nil, fmt.Errorf("factory for module %s returned nil during rollback", mod.Name())
	}
	rememberHotModuleFactory(fresh, factory)
	return fresh, nil
}
