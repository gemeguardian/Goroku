package goroku

import (
	"fmt"
	"goroku/goroku/inline"
	"log"
	"strings"
	"sync"
)

type Modules struct {
	mu         sync.RWMutex
	client     *CustomTelegramClient
	db         *Database
	modules    map[string]Module
	commands   map[string]CommandHandler
	aliases    map[string]string
	watchers   []RegisteredWatcher
	dispatcher *CommandDispatcher
	loops      map[string][]*InfiniteLoop
}

func NewModules(client *CustomTelegramClient, db *Database) *Modules {
	return &Modules{
		client:   client,
		db:       db,
		modules:  make(map[string]Module),
		commands: make(map[string]CommandHandler),
		aliases:  make(map[string]string),
		watchers: make([]RegisteredWatcher, 0),
		loops:    make(map[string][]*InfiniteLoop),
	}
}

// RegisterLoop registers an InfiniteLoop for a module and starts it if autostart is set.
func (m *Modules) RegisterLoop(loop *InfiniteLoop) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loops[loop.ModuleName] = append(m.loops[loop.ModuleName], loop)
	if loop.autostart {
		loop.Start()
	}
}

// StopModuleLoops stops all InfiniteLoops registered for the named module.
func (m *Modules) StopModuleLoops(moduleName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nameL := strings.ToLower(moduleName)
	for _, loop := range m.loops[nameL] {
		loop.Stop()
	}
	delete(m.loops, nameL)
}

func (m *Modules) RegisterModule(mod Module) error {
	name := strings.ToLower(mod.Name())
	m.mu.RLock()
	if _, ok := m.modules[name]; ok {
		m.mu.RUnlock()
		return fmt.Errorf("module %s is already registered", name)
	}
	m.mu.RUnlock()

	if withAllModules, ok := mod.(ModuleWithAllModules); ok {
		withAllModules.SetAllModules(m)
	}
	if withTranslator, ok := mod.(ModuleWithTranslator); ok {
		withTranslator.SetTranslator(NewTranslator(m.client, m.db))
	}
	m.loadModuleConfig(mod)

	err := mod.Init(m.client, m.db)
	if err != nil {
		return fmt.Errorf("failed to init module %s: %v", name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.modules[name]; ok {
		return fmt.Errorf("module %s is already registered", name)
	}

	m.modules[name] = mod

	// Register module commands
	for cmdName, handler := range mod.Commands() {
		m.commands[strings.ToLower(cmdName)] = handler
	}

	// Register aliases declared in CommandMeta
	if withMeta, ok := mod.(ModuleWithMeta); ok {
		for cmdName, meta := range withMeta.CommandMetas() {
			// Single alias
			if meta.Alias != "" {
				aliasL := strings.ToLower(meta.Alias)
				m.aliases[aliasL] = strings.ToLower(cmdName)
			}
			// Multiple aliases
			for _, a := range meta.Aliases {
				if a != "" {
					m.aliases[strings.ToLower(a)] = strings.ToLower(cmdName)
				}
			}
		}
	}

	// Register watchers
	watcherMetas := []CommandMeta{}
	if withWatcherMetas, ok := mod.(ModuleWithWatcherMetas); ok {
		watcherMetas = withWatcherMetas.WatcherMetas()
	}
	for i, w := range mod.Watchers() {
		var meta CommandMeta
		if i < len(watcherMetas) {
			meta = watcherMetas[i]
		}
		m.watchers = append(m.watchers, RegisteredWatcher{
			Handler:    w,
			ModuleName: mod.Name(),
			Meta:       meta,
		})
	}

	log.Printf("Successfully registered module: %s\n", mod.Name())
	return nil
}

func (m *Modules) loadModuleConfig(mod Module) {
	moduleName := mod.Name()
	config := make(map[string]interface{})
	if withConfig, ok := mod.(ModuleWithConfig); ok {
		for key, value := range withConfig.ConfigDefaults() {
			current := m.db.Get(moduleName, key, nil)
			if current == nil {
				m.db.Set(moduleName, key, value)
				config[key] = value
			} else {
				config[key] = current
			}
		}
	}
	if ready, ok := mod.(ModuleWithConfigReady); ok {
		if err := ready.ConfigReady(config); err != nil {
			log.Printf("ConfigReady failed for module %s: %v\n", moduleName, err)
		}
	}
}

func (m *Modules) UnloadModule(name string) error {
	m.mu.Lock()

	nameL := strings.ToLower(name)
	mod, ok := m.modules[nameL]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("module %s not found", name)
	}

	// Retrieve loops and remove them from registry
	loops := m.loops[nameL]
	delete(m.loops, nameL)

	// Unregister commands
	for cmdName := range mod.Commands() {
		delete(m.commands, strings.ToLower(cmdName))
	}

	// Remove module from list
	delete(m.modules, nameL)

	m.rebuildWatchers()
	m.mu.Unlock()

	// Stop loops outside of lock
	for _, loop := range loops {
		loop.Stop()
	}

	err := mod.OnUnload()
	if err != nil {
		log.Printf("Error during on_unload hook for %s: %v\n", name, err)
	}

	return nil
}

func (m *Modules) rebuildWatchers() {
	var newWatchers []RegisteredWatcher
	for _, mod := range m.modules {
		watcherMetas := []CommandMeta{}
		if withWatcherMetas, ok := mod.(ModuleWithWatcherMetas); ok {
			watcherMetas = withWatcherMetas.WatcherMetas()
		}
		for i, w := range mod.Watchers() {
			var meta CommandMeta
			if i < len(watcherMetas) {
				meta = watcherMetas[i]
			}
			newWatchers = append(newWatchers, RegisteredWatcher{
				Handler:    w,
				ModuleName: mod.Name(),
				Meta:       meta,
			})
		}
	}
	m.watchers = newWatchers
}

func (m *Modules) Dispatch(cmdName string) (CommandHandler, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cmdL := strings.ToLower(cmdName)
	// Check aliases first
	if realCmd, exists := m.aliases[cmdL]; exists {
		cmdL = strings.ToLower(realCmd)
	}

	handler, ok := m.commands[cmdL]
	return handler, ok
}

func (m *Modules) AddAlias(alias, cmd string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmdL := strings.ToLower(cmd)
	if _, exists := m.commands[cmdL]; !exists {
		return false
	}

	m.aliases[strings.ToLower(alias)] = cmdL
	return true
}

func (m *Modules) RemoveAlias(alias string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	aliasL := strings.ToLower(alias)
	if _, exists := m.aliases[aliasL]; exists {
		delete(m.aliases, aliasL)
		return true
	}
	return false
}

func (m *Modules) SendReady() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if im, ok := m.client.GorokuInline.(*inline.InlineManager); ok && im != nil {
		go func() {
			if err := im.RegisterManager(false, false); err != nil {
				log.Printf("Error registering inline manager: %v\n", err)
			}
		}()
	}

	for _, mod := range m.modules {
		go func(o Module) {
			if err := o.ClientReady(); err != nil {
				log.Printf("Error calling ClientReady on module %s: %v\n", o.Name(), err)
			}
		}(mod)
	}
}

func (m *Modules) GetModules() map[string]Module {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyMap := make(map[string]Module)
	for k, v := range m.modules {
		copyMap[k] = v
	}
	return copyMap
}

func (m *Modules) SetDispatcher(d *CommandDispatcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatcher = d
}

func (m *Modules) GetDispatcher() *CommandDispatcher {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dispatcher
}

// GetAliases returns a copy of the current alias map.
func (m *Modules) GetAliases() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyMap := make(map[string]string)
	for k, v := range m.aliases {
		copyMap[k] = v
	}
	return copyMap
}

// LookupByName finds a module by its Name() (case-insensitive).
func (m *Modules) LookupByName(name string) Module {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nameL := strings.ToLower(name)
	for k, mod := range m.modules {
		if k == nameL || strings.ToLower(mod.Name()) == nameL {
			return mod
		}
	}
	return nil
}

// RegisterCommand dynamically adds or replaces a single command in the dispatch table.
func (m *Modules) RegisterCommand(cmdName string, handler CommandHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commands[strings.ToLower(cmdName)] = handler
}

// UnregisterCommand removes a single command from the dispatch table.
func (m *Modules) UnregisterCommand(cmdName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.commands, strings.ToLower(cmdName))
}

func (m *Modules) GetWatchers() []RegisteredWatcher {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid data race
	res := make([]RegisteredWatcher, len(m.watchers))
	copy(res, m.watchers)
	return res
}

func (m *Modules) ReloadModuleConfig(name string) {
	m.mu.Lock()
	mod, ok := m.modules[strings.ToLower(name)]
	m.mu.Unlock()
	if !ok {
		return
	}
	m.loadModuleConfig(mod)
}
