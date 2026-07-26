package goroku

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// ErrModuleUnloadInProgress means registrations have been detached, but
// OnUnload is waiting for active handler leases to finish.
var ErrModuleUnloadInProgress = errors.New("module unload in progress")

type moduleLeaseContextKey struct{}

type moduleLeaseContext struct {
	ownerKey string
	lease    *moduleLease
	active   *atomic.Bool
}

type commandRegistration struct {
	Name      string
	Handler   CommandHandler
	Owner     Module
	OwnerName string
	ownerKey  string
	Meta      CommandMeta
	// regex is compiled once at registration from Meta.Regex.
	regex         *regexp.Regexp
	Permission    int
	hasPermission bool
	Ratelimited   bool
	Enabled       bool
	lease         *moduleLease
}

type moduleLease struct {
	mu      sync.Mutex
	active  int
	closing bool
	drained chan struct{}
}

func newModuleLease() *moduleLease {
	return &moduleLease{drained: make(chan struct{})}
}

func (l *moduleLease) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	l.active++
	return true
}

func (l *moduleLease) release() {
	l.mu.Lock()
	l.active--
	if l.closing && l.active == 0 {
		close(l.drained)
	}
	l.mu.Unlock()
}

func (l *moduleLease) close() <-chan struct{} {
	l.mu.Lock()
	if !l.closing {
		l.closing = true
		if l.active == 0 {
			close(l.drained)
		}
	}
	drained := l.drained
	l.mu.Unlock()
	return drained
}

type preparedModuleRegistration struct {
	commands []*commandRegistration
	aliases  map[string]*commandRegistration
	watchers []RegisteredWatcher
}

type moduleTeardown struct {
	done chan struct{}
	err  error
}

type Modules struct {
	mu              sync.RWMutex
	client          *CustomTelegramClient
	db              *Database
	modules         map[string]Module
	leases          map[string]*moduleLease
	commands        map[string]*commandRegistration
	aliases         map[string]*commandRegistration
	pendingModules  map[string]struct{}
	pendingCommands map[string]struct{}
	pendingAliases  map[string]struct{}
	pendingLoops    map[string][]*InfiniteLoop
	teardowns       map[string]*moduleTeardown
	watchers        []RegisteredWatcher
	// Module has no extension-provider hook today, so there is no extension
	// registry whose ownership can be captured without adding a new subsystem.
	dispatcher   *CommandDispatcher
	loops        map[string][]*InfiniteLoop
	closed       bool
	shutdownDone chan struct{}
	shutdownErr  error
}

func NewModules(client *CustomTelegramClient, db *Database) *Modules {
	return &Modules{
		client:          client,
		db:              db,
		modules:         make(map[string]Module),
		leases:          make(map[string]*moduleLease),
		commands:        make(map[string]*commandRegistration),
		aliases:         make(map[string]*commandRegistration),
		pendingModules:  make(map[string]struct{}),
		pendingCommands: make(map[string]struct{}),
		pendingAliases:  make(map[string]struct{}),
		pendingLoops:    make(map[string][]*InfiniteLoop),
		teardowns:       make(map[string]*moduleTeardown),
		watchers:        make([]RegisteredWatcher, 0),
		loops:           make(map[string][]*InfiniteLoop),
	}
}

// RegisterLoop registers an InfiniteLoop for a module and starts it if autostart is set.
func (m *Modules) RegisterLoop(loop *InfiniteLoop) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		loop.Stop()
		return
	}
	ownerKey := strings.ToLower(loop.ModuleName)
	if _, pending := m.pendingModules[ownerKey]; pending {
		m.pendingLoops[ownerKey] = append(m.pendingLoops[ownerKey], loop)
		return
	}
	m.loops[ownerKey] = append(m.loops[ownerKey], loop)
	if loop.autostart {
		loop.Start()
	}
}

// StopModuleLoops stops all InfiniteLoops registered for the named module.
func (m *Modules) StopModuleLoops(moduleName string) {
	m.mu.Lock()
	nameL := strings.ToLower(moduleName)
	loops := m.loops[nameL]
	delete(m.loops, nameL)
	m.mu.Unlock()
	for _, loop := range loops {
		loop.Stop()
	}
}

// RegisterModule lifecycle (per client):
// inject deps → Init → ConfigReady → prepare → validate → bindLegacyMaskOwners → atomic commit.
// ClientReady runs later via SendReady. Any error before commit cleans up and keeps
// the module invisible to the dispatcher.
func (m *Modules) RegisterModule(mod Module) error {
	return m.registerModule(mod, nil)
}

// RegisterModuleReady prepares a module and runs ready while its commands and
// watchers are still invisible. The complete registration is published only
// after ready succeeds.
func (m *Modules) RegisterModuleReady(mod Module, ready func() error) error {
	return m.registerModule(mod, ready)
}

func (m *Modules) registerModule(mod Module, ready func() error) error {
	if mod == nil {
		return fmt.Errorf("module is nil")
	}
	ownerName := mod.Name()
	name := strings.ToLower(ownerName)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("modules are shut down")
	}
	if _, ok := m.modules[name]; ok {
		m.mu.Unlock()
		return fmt.Errorf("module %s is already registered", name)
	}
	if _, ok := m.pendingModules[name]; ok {
		m.mu.Unlock()
		return fmt.Errorf("module %s is already being registered", name)
	}
	if teardown, ok := m.teardowns[name]; ok {
		select {
		case <-teardown.done:
			delete(m.teardowns, name)
		default:
			m.mu.Unlock()
			return fmt.Errorf("module %s: %w", name, ErrModuleUnloadInProgress)
		}
	}
	m.pendingModules[name] = struct{}{}
	m.mu.Unlock()

	if withAllModules, ok := mod.(ModuleWithAllModules); ok {
		withAllModules.SetAllModules(m)
	}
	if withTranslator, ok := mod.(ModuleWithTranslator); ok {
		withTranslator.SetTranslator(NewTranslator(m.client, m.db))
	}
	// Populate an embedded Base before Init, so a module that declares its own
	// Init still finds Client/DB/Translator ready rather than having to call
	// back into Base. Modules that do not embed Base are unaffected.
	bindModuleBase(mod, m.client, m.db)

	if err := mod.Init(m.client, m.db); err != nil {
		initErr := fmt.Errorf("failed to init module %s: %w", name, err)
		if cleanupErr := m.failRegistration(name, nil, mod); cleanupErr != nil {
			return errors.Join(initErr, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return initErr
	}

	if err := m.loadModuleConfig(mod); err != nil {
		configErr := fmt.Errorf("failed to prepare config for module %s: %w", name, err)
		if cleanupErr := m.failRegistration(name, nil, mod); cleanupErr != nil {
			return errors.Join(configErr, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return configErr
	}

	lease := newModuleLease()
	prepared, err := prepareModuleRegistration(mod, ownerName, name, lease)
	if err != nil {
		if cleanupErr := m.failRegistration(name, nil, mod); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return err
	}

	m.mu.Lock()
	if m.closed {
		m.releaseReservationLocked(name, prepared)
		m.mu.Unlock()
		closedErr := errors.New("modules are shut down")
		if cleanupErr := m.cleanupFailedModule(name, mod); cleanupErr != nil {
			return errors.Join(closedErr, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return closedErr
	}
	if err := m.validateRegistrationLocked(prepared); err != nil {
		m.releaseReservationLocked(name, prepared)
		m.mu.Unlock()
		if cleanupErr := m.cleanupFailedModule(name, mod); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return err
	}
	m.reserveRegistrationLocked(name, prepared)
	m.mu.Unlock()

	if ready != nil {
		if err := callModuleReadyHook(ready); err != nil {
			if cleanupErr := m.failRegistration(name, prepared, mod); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
			}
			return err
		}
	}

	// Database owner normalization may consult GetModules, so legacy binding
	// must run while only the namespace reservation, not m.mu, is held.
	if err := m.bindLegacyMaskOwners(prepared); err != nil {
		bindErr := fmt.Errorf("failed to persist security mask owners for module %s: %w", name, err)
		if cleanupErr := m.failRegistration(name, prepared, mod); cleanupErr != nil {
			return errors.Join(bindErr, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return bindErr
	}

	m.mu.Lock()
	if m.closed {
		m.releaseReservationLocked(name, prepared)
		m.mu.Unlock()
		closedErr := errors.New("modules are shut down")
		if cleanupErr := m.cleanupFailedModule(name, mod); cleanupErr != nil {
			return errors.Join(closedErr, fmt.Errorf("failed to clean up module %s: %w", name, cleanupErr))
		}
		return closedErr
	}
	m.releaseReservationLocked(name, prepared)
	m.modules[name] = mod
	m.leases[name] = lease
	loops := m.pendingLoops[name]
	delete(m.pendingLoops, name)
	m.loops[name] = append(m.loops[name], loops...)
	for _, reg := range prepared.commands {
		m.commands[reg.Name] = reg
	}
	for alias, reg := range prepared.aliases {
		m.aliases[alias] = reg
	}
	m.watchers = append(m.watchers, prepared.watchers...)
	for _, loop := range loops {
		if loop.autostart {
			loop.Start()
		}
	}
	m.mu.Unlock()

	L().Info("Successfully registered module", zap.String("module", ownerName))
	return nil
}

func callModuleReadyHook(ready func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("module readiness hook panicked: %v", recovered)
		}
	}()
	return ready()
}

func (m *Modules) bindLegacyMaskOwners(prepared *preparedModuleRegistration) error {
	owners, err := m.getStringMap("goroku.security", "mask_owners")
	if err != nil {
		return fmt.Errorf("read mask owners: %w", err)
	}
	changed := false
	for _, reg := range prepared.commands {
		if _, exists := owners[reg.Name]; exists {
			continue
		}
		hasBareMask := false
		for _, namespace := range []string{"goroku.security", "goroku/goroku/security"} {
			masks, err := m.getStringMap(namespace, "masks")
			if err != nil {
				return fmt.Errorf("read legacy masks from %s: %w", namespace, err)
			}
			for key, value := range masks {
				if strings.EqualFold(key, reg.Name) && value != "" {
					hasBareMask = true
					break
				}
			}
			if hasBareMask {
				break
			}
		}
		if hasBareMask {
			if owners == nil {
				owners = make(map[string]string)
			}
			owners[reg.Name] = reg.ownerKey
			changed = true
		}
	}
	if changed {
		if err := m.db.SetStringMap("goroku.security", "mask_owners", owners); err != nil {
			return err
		}
	}
	return nil
}

func (m *Modules) getStringMap(owner, key string) (map[string]string, error) {
	value, err := m.db.Get(owner, key, nil)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	switch value := value.(type) {
	case nil:
	case map[string]string:
		for key, item := range value {
			result[key] = item
		}
	case map[string]any:
		for key, item := range value {
			result[key] = fmt.Sprintf("%v", item)
		}
	}
	return result, nil
}

func (m *Modules) validateRegistrationLocked(prepared *preparedModuleRegistration) error {
	reserved := make(map[string]string)
	for _, reg := range prepared.commands {
		if _, exists := m.commands[reg.Name]; exists {
			return fmt.Errorf("command collision: %s", reg.Name)
		}
		if _, exists := m.pendingCommands[reg.Name]; exists {
			return fmt.Errorf("command collision: %s", reg.Name)
		}
		if _, exists := m.aliases[reg.Name]; exists {
			return fmt.Errorf("command %s collides with an alias", reg.Name)
		}
		if _, exists := m.pendingAliases[reg.Name]; exists {
			return fmt.Errorf("command %s collides with an alias", reg.Name)
		}
		reserved[reg.Name] = "command"
	}
	for _, alias := range sortedKeys(prepared.aliases) {
		if kind, exists := reserved[alias]; exists {
			return fmt.Errorf("alias %s collides with a %s", alias, kind)
		}
		if _, exists := m.commands[alias]; exists {
			return fmt.Errorf("alias %s collides with a command", alias)
		}
		if _, exists := m.pendingCommands[alias]; exists {
			return fmt.Errorf("alias %s collides with a command", alias)
		}
		if _, exists := m.aliases[alias]; exists {
			return fmt.Errorf("alias collision: %s", alias)
		}
		if _, exists := m.pendingAliases[alias]; exists {
			return fmt.Errorf("alias collision: %s", alias)
		}
		reserved[alias] = "alias"
	}
	return nil
}

func (m *Modules) reserveRegistrationLocked(name string, prepared *preparedModuleRegistration) {
	m.pendingModules[name] = struct{}{}
	for _, reg := range prepared.commands {
		m.pendingCommands[reg.Name] = struct{}{}
	}
	for alias := range prepared.aliases {
		m.pendingAliases[alias] = struct{}{}
	}
}

func (m *Modules) releaseReservationLocked(name string, prepared *preparedModuleRegistration) {
	delete(m.pendingModules, name)
	if prepared == nil {
		return
	}
	for _, reg := range prepared.commands {
		delete(m.pendingCommands, reg.Name)
	}
	for alias := range prepared.aliases {
		delete(m.pendingAliases, alias)
	}
}

func (m *Modules) failRegistration(name string, prepared *preparedModuleRegistration, mod Module) error {
	m.mu.Lock()
	loops := append([]*InfiniteLoop(nil), m.loops[name]...)
	pendingLoops := append([]*InfiniteLoop(nil), m.pendingLoops[name]...)
	delete(m.loops, name)
	delete(m.pendingLoops, name)
	m.releaseReservationLocked(name, prepared)
	m.mu.Unlock()
	for _, loop := range loops {
		loop.Stop()
	}
	for _, loop := range pendingLoops {
		loop.cancelPending()
	}
	if err := mod.OnUnload(); err != nil {
		L().Error("Error cleaning up failed module", zap.String("module", name), zap.Error(err))
		return err
	}
	return nil
}

func (m *Modules) cleanupFailedModule(name string, mod Module) error {
	return m.failRegistration(name, nil, mod)
}

func prepareModuleRegistration(mod Module, ownerName, ownerKey string, lease *moduleLease) (*preparedModuleRegistration, error) {
	commands := mod.Commands()
	commandNames := sortedKeys(commands)
	metas, err := normalizedCommandMetas(mod)
	if err != nil {
		return nil, err
	}
	permissions, err := normalizedPermissions(mod)
	if err != nil {
		return nil, err
	}
	ratelimits, err := normalizedRatelimits(mod)
	if err != nil {
		return nil, err
	}

	prepared := &preparedModuleRegistration{
		commands: make([]*commandRegistration, 0, len(commands)),
		aliases:  make(map[string]*commandRegistration),
	}
	seenCommands := make(map[string]struct{}, len(commands))
	for _, sourceName := range commandNames {
		name := strings.ToLower(sourceName)
		if _, exists := seenCommands[name]; exists {
			return nil, fmt.Errorf("command collision: %s", name)
		}
		seenCommands[name] = struct{}{}
		meta := cloneCommandMeta(metas[name])
		permission, hasPermission := permissions[name]
		ratelimited := meta.Ratelimit
		if limited, ok := ratelimits[name]; ok {
			ratelimited = limited
		}
		handler := commands[sourceName]
		compiled, err := compileMetaRegex(meta, "command "+name)
		if err != nil {
			return nil, err
		}
		reg := &commandRegistration{
			Name: name,
			Handler: func(msg *Message) error {
				return withModuleLeaseContext(msg, ownerKey, lease, handler)
			},
			Owner:         mod,
			OwnerName:     ownerName,
			ownerKey:      ownerKey,
			Meta:          meta,
			regex:         compiled,
			Permission:    permission,
			hasPermission: hasPermission,
			Ratelimited:   ratelimited,
			Enabled:       true,
			lease:         lease,
		}
		prepared.commands = append(prepared.commands, reg)

		aliases := append([]string(nil), meta.Aliases...)
		if meta.Alias != "" {
			aliases = append(aliases, meta.Alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			alias = strings.ToLower(alias)
			if alias == "" {
				continue
			}
			if _, exists := prepared.aliases[alias]; exists {
				return nil, fmt.Errorf("alias collision: %s", alias)
			}
			prepared.aliases[alias] = reg
		}
	}

	watchers := mod.Watchers()
	watcherMetas := []CommandMeta(nil)
	if withWatcherMetas, ok := mod.(ModuleWithWatcherMetas); ok {
		watcherMetas = withWatcherMetas.WatcherMetas()
	}
	prepared.watchers = make([]RegisteredWatcher, 0, len(watchers))
	for i, watcher := range watchers {
		var meta CommandMeta
		if i < len(watcherMetas) {
			meta = cloneCommandMeta(watcherMetas[i])
		}
		compiled, err := compileMetaRegex(meta, fmt.Sprintf("watcher %s[%d]", ownerName, i))
		if err != nil {
			return nil, err
		}
		handler := watcher
		prepared.watchers = append(prepared.watchers, RegisteredWatcher{
			Handler: func(msg *Message) error {
				return withModuleLeaseContext(msg, ownerKey, lease, handler)
			},
			ModuleName: ownerName,
			Meta:       meta,
			regex:      compiled,
			ownerKey:   ownerKey,
			lease:      lease,
		})
	}
	return prepared, nil
}

func withModuleLeaseContext(msg *Message, ownerKey string, lease *moduleLease, handler func(*Message) error) error {
	if msg == nil {
		return handler(nil)
	}
	previous := msg.ctx
	active := new(atomic.Bool)
	active.Store(true)
	msg.ctx = context.WithValue(msg.Context(), moduleLeaseContextKey{}, moduleLeaseContext{
		ownerKey: ownerKey,
		lease:    lease,
		active:   active,
	})
	defer func() {
		active.Store(false)
		msg.ctx = previous
	}()
	return handler(msg)
}

// ContextHoldsModuleLease reports whether ctx belongs to a currently executing
// handler for the named module in this registry.
func (m *Modules) ContextHoldsModuleLease(ctx context.Context, name string) bool {
	if ctx == nil {
		return false
	}
	held, ok := ctx.Value(moduleLeaseContextKey{}).(moduleLeaseContext)
	if !ok || held.active == nil || !held.active.Load() || held.ownerKey != strings.ToLower(name) {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leases[held.ownerKey] == held.lease
}

func normalizedCommandMetas(mod Module) (map[string]CommandMeta, error) {
	result := make(map[string]CommandMeta)
	withMeta, ok := mod.(ModuleWithMeta)
	if !ok {
		return result, nil
	}
	metas := withMeta.CommandMetas()
	for _, sourceName := range sortedKeys(metas) {
		name := strings.ToLower(sourceName)
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("command metadata collision: %s", name)
		}
		result[name] = cloneCommandMeta(metas[sourceName])
	}
	return result, nil
}

func normalizedPermissions(mod Module) (map[string]int, error) {
	result := make(map[string]int)
	secured, ok := mod.(SecuredModule)
	if !ok {
		return result, nil
	}
	permissions := secured.CommandPermissions()
	for _, sourceName := range sortedKeys(permissions) {
		name := strings.ToLower(sourceName)
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("command permission collision: %s", name)
		}
		result[name] = permissions[sourceName]
	}
	return result, nil
}

func normalizedRatelimits(mod Module) (map[string]bool, error) {
	result := make(map[string]bool)
	limited, ok := mod.(RatelimitedModule)
	if !ok {
		return result, nil
	}
	ratelimits := limited.RatelimitedCommands()
	for _, sourceName := range sortedKeys(ratelimits) {
		name := strings.ToLower(sourceName)
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("command rate-limit collision: %s", name)
		}
		result[name] = ratelimits[sourceName]
	}
	return result, nil
}

func cloneCommandMeta(meta CommandMeta) CommandMeta {
	meta.Aliases = append([]string(nil), meta.Aliases...)
	meta.FromID = append([]int64(nil), meta.FromID...)
	meta.ChatID = append([]int64(nil), meta.ChatID...)
	return meta
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (m *Modules) loadModuleConfig(mod Module) error {
	moduleName := mod.Name()
	config := make(map[string]any)
	defaults := make(map[string]any)

	// Prefer typed schema when present; fall back to ConfigDefaults.
	var defaultMap map[string]any
	if withSchema, ok := mod.(ModuleWithConfigSchema); ok {
		defaultMap = SchemaDefaults(withSchema.ConfigSchema())
	} else if withConfig, ok := mod.(ModuleWithConfig); ok {
		defaultMap = withConfig.ConfigDefaults()
	}

	for key, value := range defaultMap {
		current, err := m.db.Get(moduleName, key, nil)
		if err != nil {
			return NewConfigError(moduleName, key, fmt.Errorf("read: %w", err))
		}
		if current == nil {
			defaults[key] = value
			config[key] = value
		} else {
			config[key] = NormalizeConfigValue(current)
		}
	}
	if len(defaults) > 0 {
		if err := m.db.Update(map[string]map[string]any{moduleName: defaults}); err != nil {
			return NewConfigError(moduleName, "", fmt.Errorf("persist defaults: %w", err))
		}
	}
	config = NormalizeConfigMap(config)
	if ready, ok := mod.(ModuleWithConfigReady); ok {
		if err := ready.ConfigReady(config); err != nil {
			return NewConfigError(moduleName, "", fmt.Errorf("ConfigReady: %w", err))
		}
	}
	return nil
}

func (m *Modules) UnloadModule(name string) error {
	m.mu.Lock()

	nameL := strings.ToLower(name)
	mod, ok := m.modules[nameL]
	if !ok {
		if teardown, exists := m.teardowns[nameL]; exists {
			m.mu.Unlock()
			<-teardown.done
			return teardown.err
		}
		m.mu.Unlock()
		return fmt.Errorf("module %s not found", name)
	}

	// Retrieve loops and remove them from registry
	loops := m.loops[nameL]
	delete(m.loops, nameL)
	lease := m.leases[nameL]
	delete(m.leases, nameL)
	var drained <-chan struct{}
	if lease != nil {
		drained = lease.close()
	}
	teardown := &moduleTeardown{done: make(chan struct{})}
	m.teardowns[nameL] = teardown

	// Remove only registrations carrying this module's captured owner key.
	for cmdName, reg := range m.commands {
		if reg.ownerKey == nameL {
			delete(m.commands, cmdName)
		}
	}
	for alias, reg := range m.aliases {
		if reg.ownerKey == nameL {
			delete(m.aliases, alias)
		}
	}
	watchers := m.watchers[:0]
	for _, watcher := range m.watchers {
		if watcher.ownerKey != nameL {
			watchers = append(watchers, watcher)
		}
	}
	m.watchers = watchers

	// Remove module from list
	delete(m.modules, nameL)

	m.mu.Unlock()

	for _, loop := range loops {
		loop.Stop()
	}

	runTeardown := func() {
		if drained != nil {
			<-drained
		}
		err := mod.OnUnload()
		if err != nil {
			L().Error("Error during on_unload hook", zap.String("module", name), zap.Error(err))
		}
		m.mu.Lock()
		teardown.err = err
		close(teardown.done)
		m.mu.Unlock()
	}
	if drained != nil {
		select {
		case <-drained:
		default:
			go runTeardown()
			return fmt.Errorf("module %s: %w", name, ErrModuleUnloadInProgress)
		}
	}
	runTeardown()
	return teardown.err
}

func (m *Modules) Dispatch(cmdName string) (CommandHandler, bool) {
	reg, ok := m.resolveCommand(cmdName)
	if !ok {
		return nil, false
	}
	return func(msg *Message) error {
		if !reg.lease.acquire() {
			return fmt.Errorf("command %s is no longer registered", reg.Name)
		}
		defer reg.lease.release()
		return reg.Handler(msg)
	}, true
}

func (m *Modules) resolveCommand(cmdName string) (*commandRegistration, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, false
	}
	cmdL := strings.ToLower(cmdName)
	reg, ok := m.aliases[cmdL]
	if !ok {
		reg, ok = m.commands[cmdL]
	}
	return reg, ok && reg.Enabled
}

func (m *Modules) resolveCommandLease(cmdName string) (*commandRegistration, func(), bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, nil, false
	}
	cmdL := strings.ToLower(cmdName)
	reg, ok := m.aliases[cmdL]
	if !ok {
		reg, ok = m.commands[cmdL]
	}
	if !ok || !reg.Enabled || !reg.lease.acquire() {
		return nil, nil, false
	}
	return reg, reg.lease.release, true
}

func (m *Modules) AddAlias(alias, cmd string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	aliasL := strings.ToLower(alias)
	cmdL := strings.ToLower(cmd)
	reg, exists := m.commands[cmdL]
	if !exists || !reg.Enabled {
		return false
	}
	if _, exists := m.commands[aliasL]; exists {
		return false
	}
	if _, exists := m.aliases[aliasL]; exists {
		return false
	}
	if _, exists := m.pendingCommands[aliasL]; exists {
		return false
	}
	if _, exists := m.pendingAliases[aliasL]; exists {
		return false
	}
	m.aliases[aliasL] = reg
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
	type readyJob struct {
		mod   Module
		lease *moduleLease
	}

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return
	}
	names := make([]string, 0, len(m.modules))
	for name := range m.modules {
		names = append(names, name)
	}
	sort.Strings(names)
	jobs := make([]readyJob, 0, len(names))
	for _, name := range names {
		jobs = append(jobs, readyJob{mod: m.modules[name], lease: m.leases[name]})
	}
	inline := m.client.GorokuInline
	m.mu.RUnlock()

	if inline != nil {
		if err := inline.RegisterManager(false, false); err != nil {
			L().Error("Error registering inline manager", zap.Error(err))
		}
	}

	for _, job := range jobs {
		if job.lease == nil || !job.lease.acquire() {
			continue
		}
		if err := job.mod.ClientReady(); err != nil {
			L().Error("Error calling ClientReady", zap.String("module", job.mod.Name()), zap.Error(err))
		}
		job.lease.release()
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

// ModuleNames returns a stable snapshot without exposing module instances.
func (m *Modules) ModuleNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.modules))
	for name := range m.modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WithModule invokes fn while holding the same lifecycle lease used by command
// and watcher execution. The registry lock is released before user code runs.
func (m *Modules) WithModule(name string, fn func(Module)) bool {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return false
	}
	name = strings.ToLower(name)
	module, ok := m.modules[name]
	lease := m.leases[name]
	if !ok || lease == nil || !lease.acquire() {
		m.mu.RUnlock()
		return false
	}
	m.mu.RUnlock()
	defer lease.release()
	fn(module)
	return true
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

// Shutdown starts teardown exactly once. A context timeout only stops this
// caller's wait; later calls wait for and return the persistent final result.
func (m *Modules) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		done := m.shutdownDone
		m.mu.Unlock()
		if done == nil {
			return nil
		}
		select {
		case <-done:
			m.mu.RLock()
			err := m.shutdownErr
			m.mu.RUnlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.closed = true
	m.shutdownDone = make(chan struct{})
	done := m.shutdownDone
	modules := make(map[string]Module, len(m.modules))
	leases := make(map[string]*moduleLease, len(m.leases))
	teardowns := make(map[string]*moduleTeardown, len(m.teardowns)+len(m.leases))
	for name, teardown := range m.teardowns {
		teardowns[name] = teardown
	}
	for name, mod := range m.modules {
		modules[name] = mod
		lease := m.leases[name]
		leases[name] = lease
		if lease != nil {
			lease.close()
		}
		teardown := &moduleTeardown{done: make(chan struct{})}
		m.teardowns[name] = teardown
		teardowns[name] = teardown
	}
	loops := m.loops
	m.modules = make(map[string]Module)
	m.leases = make(map[string]*moduleLease)
	m.commands = make(map[string]*commandRegistration)
	m.aliases = make(map[string]*commandRegistration)
	m.watchers = nil
	m.loops = make(map[string][]*InfiniteLoop)
	m.mu.Unlock()

	var runningLoops []*InfiniteLoop
	for _, moduleLoops := range loops {
		for _, loop := range moduleLoops {
			if loop.IsRunning() {
				runningLoops = append(runningLoops, loop)
			}
			loop.Stop()
		}
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		name, mod, lease, teardown := name, modules[name], leases[name], teardowns[name]
		go func() {
			var err error
			defer func() {
				if r := recover(); r != nil {
					L().Error("Panic during on_unload hook", zap.String("module", name), zap.Any("panic", r))
					err = fmt.Errorf("panic in on_unload hook for module %s: %v", name, r)
				}
				// Accounting runs from the defer so it happens even when the
				// hook panics. OnUnload is module-supplied code; leaving
				// teardown.done unclosed would hang the collector below (and
				// therefore shutdown) forever.
				m.mu.Lock()
				teardown.err = err
				close(teardown.done)
				m.mu.Unlock()
			}()
			if lease != nil {
				<-lease.drained
			}
			err = mod.OnUnload()
			if err != nil {
				L().Error("Error during on_unload hook", zap.String("module", name), zap.Error(err))
			}
		}()
	}
	teardownNames := make([]string, 0, len(teardowns))
	for name := range teardowns {
		teardownNames = append(teardownNames, name)
	}
	sort.Strings(teardownNames)
	go func() {
		for _, loop := range runningLoops {
			<-loop.Stopped()
		}
		var errs []error
		for _, name := range teardownNames {
			teardown := teardowns[name]
			<-teardown.done
			if teardown.err != nil {
				errs = append(errs, fmt.Errorf("unload module %s: %w", name, teardown.err))
			}
		}
		m.mu.Lock()
		m.shutdownErr = errors.Join(errs...)
		close(done)
		m.mu.Unlock()
	}()

	select {
	case <-done:
		m.mu.RLock()
		err := m.shutdownErr
		m.mu.RUnlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetAliases returns a copy of the current alias map.
func (m *Modules) GetAliases() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyMap := make(map[string]string)
	for k, v := range m.aliases {
		copyMap[k] = v.Name
	}
	return copyMap
}

// LookupByName finds a module by its Name() (case-insensitive).
func (m *Modules) LookupByName(name string) Module {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.modules[strings.ToLower(name)]
}

// RegisterCommand re-enables a command declared by a loaded module. The handler
// argument is retained for API compatibility; registration never rebinds the
// handler captured from Commands during module registration.
func (m *Modules) RegisterCommand(cmdName string, handler CommandHandler) {
	_ = handler
	m.mu.Lock()
	defer m.mu.Unlock()
	cmdName = strings.ToLower(cmdName)
	if reg, ok := m.commands[cmdName]; ok {
		replacement := *reg
		replacement.Enabled = true
		m.replaceRegistrationLocked(reg, &replacement)
	}
}

// UnregisterCommand disables a command while preserving its owner and aliases.
func (m *Modules) UnregisterCommand(cmdName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reg, ok := m.commands[strings.ToLower(cmdName)]; ok {
		replacement := *reg
		replacement.Enabled = false
		m.replaceRegistrationLocked(reg, &replacement)
	}
}

func (m *Modules) replaceRegistrationLocked(old, replacement *commandRegistration) {
	m.commands[old.Name] = replacement
	for alias, reg := range m.aliases {
		if reg == old {
			m.aliases[alias] = replacement
		}
	}
}

func (m *Modules) GetWatchers() []RegisteredWatcher {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid data race
	res := make([]RegisteredWatcher, len(m.watchers))
	for i, watcher := range m.watchers {
		res[i] = watcher
		res[i].Meta = cloneCommandMeta(watcher.Meta)
	}
	return res
}

func (m *Modules) ReloadModuleConfig(name string) {
	m.mu.Lock()
	mod, ok := m.modules[strings.ToLower(name)]
	m.mu.Unlock()
	if !ok {
		return
	}
	if err := m.loadModuleConfig(mod); err != nil {
		L().Error("Failed to reload module config", zap.String("module", name), zap.Error(err))
	}
}
