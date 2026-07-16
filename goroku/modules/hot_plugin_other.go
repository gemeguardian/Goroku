//go:build !linux && !android

package modules

import (
	"fmt"
	"goroku/goroku"
)

func RegisterModulesHot(msg *goroku.Message, structNames []string) error {
	return fmt.Errorf("hot module loading is only supported on Linux and Android")
}

func HotLoadStructs(loader *goroku.Modules, structNames []string) error {
	return fmt.Errorf("hot module loading is only supported on Linux and Android")
}

func prepareHotModule(structName, sourcePath string) (goroku.Module, error) {
	return nil, fmt.Errorf("hot module loading is only supported on Linux and Android")
}

func replacePreparedHotModule(loader *goroku.Modules, mod goroku.Module) error {
	return fmt.Errorf("hot module loading is only supported on Linux and Android")
}

func validateHotModuleCompilation(structName string, source []byte) error {
	return fmt.Errorf("hot module loading is only supported on Linux and Android")
}
