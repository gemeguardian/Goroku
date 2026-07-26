package goroku

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type registrationTestModule struct {
	name        string
	commands    map[string]CommandHandler
	metas       map[string]CommandMeta
	permissions map[string]int
	ratelimited map[string]bool
	watchers    []WatcherHandler
	watcherMeta []CommandMeta
}

type stagedLoopTestModule struct {
	registrationTestModule
	modules *Modules
	ticked  chan struct{}
	once    sync.Once
}

func (m *stagedLoopTestModule) SetAllModules(modules *Modules) { m.modules = modules }
func (m *stagedLoopTestModule) Init(*CustomTelegramClient, *Database) error {
	loop := NewInfiniteLoop(func() error {
		m.once.Do(func() { close(m.ticked) })
		return nil
	}, time.Millisecond, m.name, true)
	m.modules.RegisterLoop(loop)
	return nil
}

func (m *registrationTestModule) Name() string                                { return m.name }
func (m *registrationTestModule) Strings() map[string]string                  { return nil }
func (m *registrationTestModule) Init(*CustomTelegramClient, *Database) error { return nil }
func (m *registrationTestModule) ClientReady() error                          { return nil }
func (m *registrationTestModule) OnUnload() error                             { return nil }
func (m *registrationTestModule) OnDlmod() error                              { return nil }
func (m *registrationTestModule) Commands() map[string]CommandHandler         { return m.commands }
func (m *registrationTestModule) Watchers() []WatcherHandler                  { return m.watchers }
func (m *registrationTestModule) CommandMetas() map[string]CommandMeta        { return m.metas }
func (m *registrationTestModule) CommandPermissions() map[string]int          { return m.permissions }
func (m *registrationTestModule) RatelimitedCommands() map[string]bool        { return m.ratelimited }
func (m *registrationTestModule) WatcherMetas() []CommandMeta                 { return m.watcherMeta }

func testHandler(marker string) CommandHandler {
	return func(msg *Message) error {
		msg.Text = marker
		return nil
	}
}

func TestRegisterModuleRejectsCommandAndAliasCollisionsAtomically(t *testing.T) {
	tests := []struct {
		name        string
		firstCmd    string
		firstAlias  string
		secondCmd   string
		secondAlias string
		collision   string
	}{
		{name: "command-command", firstCmd: "shared", secondCmd: "SHARED", collision: "command collision: shared"},
		{name: "command-alias", firstCmd: "first", firstAlias: "taken", secondCmd: "TAKEN", collision: "command taken collides with an alias"},
		{name: "alias-command", firstCmd: "taken", secondCmd: "second", secondAlias: "TAKEN", collision: "alias taken collides with a command"},
		{name: "alias-alias", firstCmd: "first", firstAlias: "same", secondCmd: "second", secondAlias: "SAME", collision: "alias collision: same"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := initializedTestDatabase(t, NewDatabase(42))
			client := NewCustomTelegramClient(42)
			modules := NewModules(client, db)
			first := &registrationTestModule{
				name: "first", commands: map[string]CommandHandler{tc.firstCmd: testHandler("first")},
				metas: map[string]CommandMeta{tc.firstCmd: {Alias: tc.firstAlias}},
			}
			second := &registrationTestModule{
				name: "second", commands: map[string]CommandHandler{tc.secondCmd: testHandler("second")},
				metas: map[string]CommandMeta{tc.secondCmd: {Alias: tc.secondAlias}},
			}

			if err := modules.RegisterModule(first); err != nil {
				t.Fatalf("register first: %v", err)
			}
			err := modules.RegisterModule(second)
			if err == nil || err.Error() != tc.collision {
				t.Fatalf("collision error = %v, want %q", err, tc.collision)
			}
			if modules.LookupByName(second.Name()) != nil {
				t.Fatal("colliding module was partially registered")
			}
			reg, ok := modules.resolveCommand(tc.firstCmd)
			if !ok || reg.Owner != first {
				t.Fatal("first registration was changed by collision")
			}
		})
	}
}

func TestRegisterModuleReadyPublishesOnlyAfterReadiness(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	mod := &registrationTestModule{
		name:     "staged",
		commands: map[string]CommandHandler{"staged": testHandler("ready")},
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- modules.RegisterModuleReady(mod, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	if modules.LookupByName(mod.Name()) != nil {
		t.Fatal("staged module became visible before readiness completed")
	}
	if _, ok := modules.Dispatch("staged"); ok {
		t.Fatal("staged command became visible before readiness completed")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if modules.LookupByName(mod.Name()) != mod {
		t.Fatal("ready module was not published")
	}
	if _, ok := modules.Dispatch("staged"); !ok {
		t.Fatal("ready command was not published")
	}
}

func TestRegisterModuleReadyFailureReleasesNamespace(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	cause := errors.New("readiness failed")
	failed := &registrationTestModule{name: "staged-failure", commands: map[string]CommandHandler{"retry": testHandler("failed")}}
	if err := modules.RegisterModuleReady(failed, func() error { return cause }); !errors.Is(err, cause) {
		t.Fatalf("readiness error = %v, want cause", err)
	}
	if modules.LookupByName(failed.Name()) != nil {
		t.Fatal("failed staged module was published")
	}
	retry := &registrationTestModule{name: failed.Name(), commands: map[string]CommandHandler{"retry": testHandler("ok")}}
	if err := modules.RegisterModule(retry); err != nil {
		t.Fatalf("namespace remained reserved after readiness failure: %v", err)
	}
}

func TestRegisterModuleReadyDefersAutostartLoopsUntilCommit(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	mod := &stagedLoopTestModule{
		registrationTestModule: registrationTestModule{name: "staged-loop"},
		ticked:                 make(chan struct{}),
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- modules.RegisterModuleReady(mod, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	select {
	case <-mod.ticked:
		t.Fatal("staged loop ran before module publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-mod.ticked:
	case <-time.After(time.Second):
		t.Fatal("autostart loop did not run after module publication")
	}
	if err := modules.UnloadModule(mod.Name()); err != nil {
		t.Fatal(err)
	}
}

func TestUnloadRemovesOnlyOwnerRegistrationsAndAliases(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	first := &registrationTestModule{
		name: "first", commands: map[string]CommandHandler{"first": testHandler("first")},
		metas: map[string]CommandMeta{"first": {Alias: "first_alias"}},
	}
	second := &registrationTestModule{
		name: "second", commands: map[string]CommandHandler{"second": testHandler("second")},
		metas: map[string]CommandMeta{"second": {Alias: "second_alias"}},
	}
	if err := modules.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if err := modules.RegisterModule(second); err != nil {
		t.Fatal(err)
	}
	if !modules.AddAlias("dynamic_first", "first") {
		t.Fatal("add owner alias")
	}

	// A module may expose a mutable Commands map. Unload must use captured
	// ownership rather than its current contents.
	first.commands["second"] = testHandler("foreign")
	if err := modules.UnloadModule("first"); err != nil {
		t.Fatal(err)
	}
	reg, ok := modules.resolveCommand("second_alias")
	if !ok || reg.Owner != second || reg.Name != "second" {
		t.Fatal("unload removed or changed another owner's command")
	}
	for _, alias := range []string{"first_alias", "dynamic_first"} {
		if _, ok := modules.resolveCommand(alias); ok {
			t.Fatalf("owner alias %q survived unload", alias)
		}
	}
}

func TestRegistrationKeepsHandlerOwnerSecurityMetadataAndRatelimitTogether(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
	}
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	first := &registrationTestModule{
		name:        "first",
		commands:    map[string]CommandHandler{"shared": testHandler("first")},
		metas:       map[string]CommandMeta{"shared": {Alias: "shared_alias", OnlyPM: true}},
		permissions: map[string]int{"shared": EVERYONE},
		ratelimited: map[string]bool{"shared": false},
	}
	second := &registrationTestModule{
		name:        "second",
		commands:    map[string]CommandHandler{"shared": testHandler("second")},
		metas:       map[string]CommandMeta{"shared": {OnlyOwner: true}},
		permissions: map[string]int{"shared": OWNER},
		ratelimited: map[string]bool{"shared": true},
	}
	if err := modules.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if err := modules.RegisterModule(second); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected collision, got %v", err)
	}
	first.commands["shared"] = testHandler("mutated")
	first.metas["shared"] = CommandMeta{OnlyOwner: true}
	first.permissions["shared"] = OWNER
	first.ratelimited["shared"] = true
	first.name = "mutated"
	if modules.LookupByName("mutated") != nil || modules.LookupByName("first") != first {
		t.Fatal("module lookup followed mutable owner name")
	}

	reg, ok := modules.resolveCommand("shared_alias")
	if !ok || reg.Owner != first || reg.Handler == nil {
		t.Fatal("alias did not resolve to the complete first registration")
	}
	dispatched, ok := modules.Dispatch("SHARED_ALIAS")
	if !ok {
		t.Fatal("Dispatch did not resolve the registered alias")
	}
	dispatchMsg := &Message{}
	if err := dispatched(dispatchMsg); err != nil || dispatchMsg.Text != "first" {
		t.Fatalf("Dispatch selected wrong handler: text=%q err=%v", dispatchMsg.Text, err)
	}
	msg := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}
	cd := NewCommandDispatcher(modules, client, db)
	t.Cleanup(cd.security.Stop)
	if !cd.security.checkRegistration(msg, reg) {
		t.Fatal("security did not use registered owner's EVERYONE permission")
	}
	if !cd.handleRegistrationTags(msg, reg) {
		t.Fatal("registered owner's OnlyPM metadata did not match")
	}
	if cd.handleRegistrationTags(&Message{SenderID: 100, IsGroup: true}, reg) {
		t.Fatal("metadata came from a different owner")
	}
	cd.rateLimiter = newTestRateLimiter(t, &fakeRateLimitClock{now: time.Unix(1, 0)}, 100, 100, 2)
	if !cd.handleRegistrationRatelimit(msg, reg) {
		t.Fatal("first rate-limit check unexpectedly failed")
	}
	if got := cd.rateLimiter.users[msg.SenderID].used; got != 2 {
		t.Fatalf("rate-limit severity = %d, want first owner's non-limited severity 2", got)
	}
	if err := reg.Handler(msg); err != nil || msg.Text != "first" {
		t.Fatalf("wrong handler dispatched: text=%q err=%v", msg.Text, err)
	}
}

func TestRegistrationDeepCopiesMetadata(t *testing.T) {
	modules := newInitializedTestModules(t)
	from := []int64{100}
	chats := []int64{200}
	aliases := []string{"copy_alias"}
	module := &registrationTestModule{
		name:     "copies",
		commands: map[string]CommandHandler{"copy": testHandler("copy")},
		metas: map[string]CommandMeta{"copy": {
			Aliases: aliases,
			FromID:  from,
			ChatID:  chats,
		}},
	}
	if err := modules.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	aliases[0] = "changed"
	from[0] = 101
	chats[0] = 201

	reg, ok := modules.resolveCommand("copy_alias")
	if !ok {
		t.Fatal("captured alias changed with module metadata")
	}
	if reg.Meta.FromID[0] != 100 || reg.Meta.ChatID[0] != 200 || reg.Meta.Aliases[0] != "copy_alias" {
		t.Fatalf("registration metadata was not copied: %+v", reg.Meta)
	}
}

func TestUnloadFiltersCapturedWatcherOwnership(t *testing.T) {
	modules := newInitializedTestModules(t)
	firstHandler := func(*Message) error { return nil }
	secondHandler := func(*Message) error { return nil }
	first := &registrationTestModule{
		name:        "first",
		commands:    map[string]CommandHandler{},
		watchers:    []WatcherHandler{firstHandler},
		watcherMeta: []CommandMeta{{FromID: []int64{10}}},
	}
	second := &registrationTestModule{
		name:        "second",
		commands:    map[string]CommandHandler{},
		watchers:    []WatcherHandler{secondHandler},
		watcherMeta: []CommandMeta{{FromID: []int64{20}}},
	}
	if err := modules.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if err := modules.RegisterModule(second); err != nil {
		t.Fatal(err)
	}

	first.name = "second"
	first.watchers = second.watchers
	first.watcherMeta[0].FromID[0] = 99
	watchers := modules.GetWatchers()
	if watchers[0].ModuleName != "first" || watchers[0].Meta.FromID[0] != 10 {
		t.Fatalf("watcher registration was not captured: %+v", watchers[0])
	}
	watchers[0].Meta.FromID[0] = 77
	if modules.GetWatchers()[0].Meta.FromID[0] != 10 {
		t.Fatal("GetWatchers exposed mutable registry metadata")
	}

	if err := modules.UnloadModule("first"); err != nil {
		t.Fatal(err)
	}
	watchers = modules.GetWatchers()
	if len(watchers) != 1 || watchers[0].ModuleName != "second" || watchers[0].Meta.FromID[0] != 20 {
		t.Fatalf("unload removed the wrong watcher: %+v", watchers)
	}
}

func TestRegistrationRejectsCaseFoldedModuleMapInputsDeterministically(t *testing.T) {
	tests := []struct {
		name string
		mod  *registrationTestModule
		want string
	}{
		{
			name: "commands",
			mod: &registrationTestModule{name: "duplicate", commands: map[string]CommandHandler{
				"PING": testHandler("upper"), "ping": testHandler("lower"),
			}},
			want: "command collision: ping",
		},
		{
			name: "permissions",
			mod: &registrationTestModule{name: "duplicate", commands: map[string]CommandHandler{"ping": testHandler("ping")},
				permissions: map[string]int{"PING": OWNER, "ping": EVERYONE}},
			want: "command permission collision: ping",
		},
		{
			name: "rate limits",
			mod: &registrationTestModule{name: "duplicate", commands: map[string]CommandHandler{"ping": testHandler("ping")},
				ratelimited: map[string]bool{"PING": true, "ping": false}},
			want: "command rate-limit collision: ping",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				modules := newInitializedTestModules(t)
				if err := modules.RegisterModule(tc.mod); err == nil || err.Error() != tc.want {
					t.Fatalf("attempt %d: error = %v, want %q", i, err, tc.want)
				}
				if modules.LookupByName("duplicate") != nil {
					t.Fatal("failed preparation changed registry")
				}
			}
		})
	}
}

func TestDispatchPolicyDoesNotReadModuleMapsAfterRegistration(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"owner":         []any{int64(42)},
		"all_users":     []any{},
		"bounding_mask": float64(ALL),
	}
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	module := &registrationTestModule{
		name:        "stable",
		commands:    map[string]CommandHandler{"stable": testHandler("stable")},
		metas:       map[string]CommandMeta{"stable": {OnlyPM: true}},
		permissions: map[string]int{"stable": EVERYONE},
		ratelimited: map[string]bool{"stable": false},
	}
	if err := modules.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	reg, ok := modules.resolveCommand("stable")
	if !ok {
		t.Fatal("command not registered")
	}
	dispatcher := NewCommandDispatcher(modules, client, db)
	t.Cleanup(dispatcher.security.Stop)
	dispatcher.rateLimiter = newTestRateLimiter(t, &fakeRateLimitClock{now: time.Unix(1, 0)}, 1_000, 1_000, 2)
	msg := &Message{SenderID: 100, ChatID: 100, IsPrivate: true}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			module.name = "mutated"
			module.commands["stable"] = testHandler("mutated")
			module.metas["stable"] = CommandMeta{OnlyOwner: true}
			module.permissions["stable"] = OWNER
			module.ratelimited["stable"] = true
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if !dispatcher.security.checkRegistration(msg, reg) {
				t.Errorf("captured permission changed on iteration %d", i)
				return
			}
			if !dispatcher.handleRegistrationTags(msg, reg) {
				t.Errorf("captured metadata changed on iteration %d", i)
				return
			}
			dispatcher.handleRegistrationRatelimit(msg, reg)
			if reg.OwnerName != "stable" || reg.Ratelimited {
				t.Errorf("captured owner/rate-limit changed on iteration %d", i)
				return
			}
		}
	}()
	wg.Wait()
}

type registrationLifecycleModule struct {
	name        string
	commands    map[string]CommandHandler
	watchers    []WatcherHandler
	init        func(*CustomTelegramClient) error
	onUnload    func() error
	initCalls   atomic.Int32
	unloadCalls atomic.Int32
}

func newInitializedTestModules(t *testing.T) *Modules {
	t.Helper()
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	loader := NewModules(client, db)
	client.GorokuDB = db
	client.Loader = loader
	return loader
}

type registrationConfigModule struct {
	*registrationLifecycleModule
	configReady atomic.Int32
	ready       func() error
}

func (m *registrationConfigModule) ConfigDefaults() map[string]any {
	return map[string]any{"enabled": true, "limit": 10}
}

func (m *registrationConfigModule) ConfigReady(map[string]any) error {
	m.configReady.Add(1)
	if m.ready != nil {
		return m.ready()
	}
	return nil
}

func (m *registrationLifecycleModule) Name() string               { return m.name }
func (m *registrationLifecycleModule) Strings() map[string]string { return nil }
func (m *registrationLifecycleModule) Init(client *CustomTelegramClient, _ *Database) error {
	m.initCalls.Add(1)
	if m.init != nil {
		return m.init(client)
	}
	return nil
}
func (m *registrationLifecycleModule) ClientReady() error { return nil }
func (m *registrationLifecycleModule) OnUnload() error {
	m.unloadCalls.Add(1)
	if m.onUnload != nil {
		return m.onUnload()
	}
	return nil
}
func (m *registrationLifecycleModule) OnDlmod() error                      { return nil }
func (m *registrationLifecycleModule) Commands() map[string]CommandHandler { return m.commands }
func (m *registrationLifecycleModule) Watchers() []WatcherHandler          { return m.watchers }

func TestRegistrationCollisionCleansUpAfterInit(t *testing.T) {
	modules := newInitializedTestModules(t)
	first := &registrationLifecycleModule{name: "first", commands: map[string]CommandHandler{"shared": testHandler("first")}}
	second := &registrationLifecycleModule{name: "second", commands: map[string]CommandHandler{"shared": testHandler("second")}}
	if err := modules.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if err := modules.RegisterModule(second); err == nil {
		t.Fatal("expected command collision")
	}
	if second.initCalls.Load() != 1 || second.unloadCalls.Load() != 1 {
		t.Fatalf("rejected module lifecycle calls: init=%d unload=%d", second.initCalls.Load(), second.unloadCalls.Load())
	}
	if modules.LookupByName(second.Name()) != nil {
		t.Fatal("colliding module remained visible after cleanup")
	}
	if _, ok := modules.Dispatch("shared"); !ok {
		t.Fatal("first module command was removed by collision cleanup")
	}
}

func TestFailedRegistrationStopsOwnedLoopsAndRunsCleanup(t *testing.T) {
	db := NewDatabase(42)
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)
	client.Loader = modules
	var loop *InfiniteLoop
	module := &registrationLifecycleModule{
		name:     "failed",
		commands: map[string]CommandHandler{"failed": testHandler("failed")},
		init: func(client *CustomTelegramClient) error {
			loop = NewInfiniteLoop(func() error { return nil }, time.Hour, "failed", true)
			client.Loader.RegisterLoop(loop)
			return errors.New("init failed")
		},
	}
	if err := modules.RegisterModule(module); err == nil {
		t.Fatal("expected initialization failure")
	}
	if module.unloadCalls.Load() != 1 {
		t.Fatalf("OnUnload calls = %d, want 1", module.unloadCalls.Load())
	}
	select {
	case <-loop.Stopped():
	case <-time.After(time.Second):
		t.Fatal("failed module loop was not stopped")
	}
	if modules.LookupByName("failed") != nil || len(modules.GetWatchers()) != 0 {
		t.Fatal("failed module left registrations behind")
	}
	if _, ok := modules.Dispatch("failed"); ok {
		t.Fatal("failed module command survived cleanup")
	}
}

func TestUnloadCallsOnUnloadWithoutLoaderMutex(t *testing.T) {
	modules := newInitializedTestModules(t)
	module := &registrationLifecycleModule{name: "reentrant", commands: map[string]CommandHandler{}}
	module.onUnload = func() error {
		_ = modules.GetModules()
		modules.StopModuleLoops("reentrant")
		return nil
	}
	if err := modules.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- modules.UnloadModule("reentrant") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("OnUnload deadlocked on loader mutex")
	}
}

func TestRegisterModuleWithInitializedEmptyDatabaseDoesNotDeadlock(t *testing.T) {
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	t.Cleanup(func() { BaseDir = originalBaseDir })

	db := NewDatabase(42)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	client := NewCustomTelegramClient(42)
	client.GorokuDB = db
	loader := NewModules(client, db)
	client.Loader = loader
	db.AttachRuntime(loader, loader, newTelegramAssetTransport(client))
	module := &registrationLifecycleModule{name: "initialized", commands: map[string]CommandHandler{"initialized": testHandler("ok")}}

	done := make(chan error, 1)
	go func() { done <- loader.RegisterModule(module) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("registration deadlocked through database owner normalization")
	}
}

func TestRegisterModulePreservesInitAndCleanupCauses(t *testing.T) {
	initCause := databaseError("get", "failed", "init", "", ErrDatabaseClosed, nil)
	cleanupCause := databaseError("set", "failed", "cleanup", "", ErrDatabaseInvalidValue, nil)
	module := &registrationLifecycleModule{
		name:     "failed",
		commands: map[string]CommandHandler{},
		init: func(*CustomTelegramClient) error {
			return initCause
		},
		onUnload: func() error {
			return cleanupCause
		},
	}
	loader := newInitializedTestModules(t)

	err := loader.RegisterModule(module)
	if err == nil {
		t.Fatal("expected registration failure")
	}
	if !errors.Is(err, ErrDatabaseClosed) || !errors.Is(err, ErrDatabaseInvalidValue) {
		t.Fatalf("registration error lost a cause: %v", err)
	}
	var databaseErr *DatabaseError
	if !errors.As(err, &databaseErr) {
		t.Fatalf("registration error does not expose DatabaseError: %T", err)
	}
}

func TestRegisterModuleDefaultPersistenceFailureIsAtomic(t *testing.T) {
	db := NewDatabase(42)
	db.dbFile = filepath.Join(t.TempDir(), "database.json")
	db.initialized = true
	db.writeLocal = func(string, []byte) error { return errors.New("injected default write failure") }
	loader := NewModules(NewCustomTelegramClient(42), db)
	module := &registrationConfigModule{registrationLifecycleModule: &registrationLifecycleModule{
		name: "defaults", commands: map[string]CommandHandler{"defaults": testHandler("ok")},
	}}

	err := loader.RegisterModule(module)
	if !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("registration error = %v, want persistence failure", err)
	}
	if module.initCalls.Load() != 1 || module.configReady.Load() != 0 || module.unloadCalls.Load() != 1 {
		t.Fatalf("lifecycle after default failure: init=%d config=%d unload=%d", module.initCalls.Load(), module.configReady.Load(), module.unloadCalls.Load())
	}
	if loader.LookupByName("defaults") != nil {
		t.Fatal("module was registered after default persistence failure")
	}
	if _, ok := loader.Dispatch("defaults"); ok {
		t.Fatal("command was published after default persistence failure")
	}
	if _, err := db.Get("defaults", "enabled", nil); err != nil && !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatal(err)
	}
	if _, exists := db.Dump()["defaults"]; exists {
		t.Fatal("failed atomic default update changed database state")
	}
}

func TestRegisterModuleLegacyMaskPersistenceFailureAbortsPublication(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	db.data["goroku.security"] = map[string]any{
		"masks": map[string]any{"legacy": "owner"},
	}
	db.writeLocal = func(string, []byte) error { return errors.New("injected mask owner write failure") }
	loader := NewModules(NewCustomTelegramClient(42), db)
	module := &registrationLifecycleModule{
		name: "legacy-owner", commands: map[string]CommandHandler{"legacy": testHandler("ok")},
	}

	err := loader.RegisterModule(module)
	if !errors.Is(err, ErrDatabasePersistence) {
		t.Fatalf("registration error = %v, want persistence failure", err)
	}
	if module.initCalls.Load() != 1 || module.unloadCalls.Load() != 1 {
		t.Fatalf("failed registration lifecycle: init=%d unload=%d", module.initCalls.Load(), module.unloadCalls.Load())
	}
	if loader.LookupByName(module.Name()) != nil {
		t.Fatal("module published after mask owner persistence failure")
	}
	if _, ok := loader.Dispatch("legacy"); ok {
		t.Fatal("command published after mask owner persistence failure")
	}
	if _, exists := db.GetStringMap("goroku.security", "mask_owners", nil)["legacy"]; exists {
		t.Fatal("failed mask owner write changed database state")
	}
}

func TestRegisterModuleConfigReadyErrorPreservesCauseWithoutLoaderLock(t *testing.T) {
	loader := newInitializedTestModules(t)
	cause := errors.New("config rejected")
	module := &registrationConfigModule{registrationLifecycleModule: &registrationLifecycleModule{
		name: "config-ready", commands: map[string]CommandHandler{"config-ready": testHandler("ok")},
	}}
	module.ready = func() error {
		_ = loader.GetModules()
		return cause
	}

	done := make(chan error, 1)
	go func() { done <- loader.RegisterModule(module) }()
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("registration error = %v, want ConfigReady cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConfigReady callback ran under loader mutex")
	}
	if module.initCalls.Load() != 1 || module.unloadCalls.Load() != 1 {
		t.Fatalf("lifecycle after ConfigReady failure: init=%d unload=%d", module.initCalls.Load(), module.unloadCalls.Load())
	}
	if loader.LookupByName(module.Name()) != nil {
		t.Fatal("module registered after ConfigReady failure")
	}
	if _, ok := loader.Dispatch("config-ready"); ok {
		t.Fatal("command published after ConfigReady failure")
	}
}

func TestRegisterModuleLifecycleOrderInitBeforeConfigReady(t *testing.T) {
	loader := newInitializedTestModules(t)
	var order []string
	var mu sync.Mutex
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	module := &registrationConfigModule{registrationLifecycleModule: &registrationLifecycleModule{
		name:     "order",
		commands: map[string]CommandHandler{"order": testHandler("ok")},
		init: func(*CustomTelegramClient) error {
			record("init")
			return nil
		},
	}}
	module.ready = func() error {
		record("config")
		return nil
	}
	if err := loader.RegisterModule(module); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "init" || got[1] != "config" {
		t.Fatalf("lifecycle order = %v, want [init config]", got)
	}
	if loader.LookupByName("order") == nil {
		t.Fatal("module not committed after successful lifecycle")
	}
}

func TestModuleFactoryConstructsIndependentInstances(t *testing.T) {
	var constructed atomic.Int32
	factory := ModuleFactory(func() Module {
		n := constructed.Add(1)
		return &registrationLifecycleModule{
			name:     fmt.Sprintf("factory-%d", n),
			commands: map[string]CommandHandler{fmt.Sprintf("cmd%d", n): testHandler("ok")},
		}
	})
	first := factory()
	second := factory()
	if first == second {
		t.Fatal("factory returned the same module instance")
	}
	loaderA := newInitializedTestModules(t)
	loaderB := newInitializedTestModules(t)
	if err := loaderA.RegisterModule(first); err != nil {
		t.Fatal(err)
	}
	if err := loaderB.RegisterModule(second); err != nil {
		t.Fatal(err)
	}
	if loaderA.LookupByName(first.Name()) != first {
		t.Fatal("loader A missing first factory instance")
	}
	if loaderB.LookupByName(second.Name()) != second {
		t.Fatal("loader B missing second factory instance")
	}
	if loaderA.LookupByName(second.Name()) != nil || loaderB.LookupByName(first.Name()) != nil {
		t.Fatal("factory instances leaked across clients")
	}
}

type registrationReadyModule struct {
	*registrationLifecycleModule
	ready func() error
}

func (m *registrationReadyModule) ClientReady() error {
	if m.ready != nil {
		return m.ready()
	}
	return nil
}

func TestSendReadyRunsClientReadySynchronouslyUnderLease(t *testing.T) {
	loader := newInitializedTestModules(t)
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var readyCalls atomic.Int32
	started := make(chan struct{})
	var startedOnce sync.Once

	makeModule := func(name string) *registrationReadyModule {
		return &registrationReadyModule{
			registrationLifecycleModule: &registrationLifecycleModule{
				name:     name,
				commands: map[string]CommandHandler{name: testHandler(name)},
			},
			ready: func() error {
				cur := concurrent.Add(1)
				for {
					prev := maxConcurrent.Load()
					if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
						break
					}
				}
				startedOnce.Do(func() { close(started) })
				time.Sleep(20 * time.Millisecond)
				concurrent.Add(-1)
				readyCalls.Add(1)
				return nil
			},
		}
	}
	if err := loader.RegisterModule(makeModule("ready-a")); err != nil {
		t.Fatal(err)
	}
	if err := loader.RegisterModule(makeModule("ready-b")); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		loader.SendReady()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ClientReady never started")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendReady returned before ClientReady finished")
	}
	if readyCalls.Load() != 2 {
		t.Fatalf("ClientReady calls = %d, want 2", readyCalls.Load())
	}
	if maxConcurrent.Load() != 1 {
		t.Fatalf("ClientReady max concurrency = %d, want 1 (sequential)", maxConcurrent.Load())
	}
}
