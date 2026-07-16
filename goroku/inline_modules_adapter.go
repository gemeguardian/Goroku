package goroku

import "goroku/goroku/inline"

// Compile-time consumer-port assertions (M7.2) for the inline package.
var (
	_ inline.InlineModules   = (*InlineModulesAdapter)(nil)
	_ inline.InlineUserBot   = (*CustomTelegramClient)(nil)
	_ inline.Database        = (*Database)(nil)
	_ inline.SecurityChecker = (*SecurityManager)(nil)
)

// InlineModulesAdapter adapts goroku.Modules so it satisfies inline.InlineModules.
// InlineModules requires map[string]any while Modules.GetModules returns
// map[string]Module. Since every Module is an interface, this only needs a copy.
type InlineModulesAdapter struct {
	modules *Modules
}

// NewInlineModulesAdapter wraps a goroku Modules instance for the inline package.
func NewInlineModulesAdapter(m *Modules) *InlineModulesAdapter {
	return &InlineModulesAdapter{modules: m}
}

func (a *InlineModulesAdapter) ModuleNames() []string {
	return a.modules.ModuleNames()
}

func (a *InlineModulesAdapter) WithModule(name string, fn func(any)) bool {
	return a.modules.WithModule(name, func(module Module) { fn(module) })
}
