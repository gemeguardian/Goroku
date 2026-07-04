package goroku

import "goroku/goroku/inline"

var _ inline.InlineModules = (*InlineModulesAdapter)(nil)

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

// GetModules returns a copy of the registered modules as map[string]any.
func (a *InlineModulesAdapter) GetModules() map[string]any {
	mods := a.modules.GetModules()
	result := make(map[string]any, len(mods))
	for k, v := range mods {
		result[k] = v
	}
	return result
}
