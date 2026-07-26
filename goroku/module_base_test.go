package goroku

import "testing"

// minimalBaseModule is the whole point of Base: a working module that declares
// nothing but its name and its commands.
type minimalBaseModule struct {
	Base
}

func (m *minimalBaseModule) Name() string { return "Minimal" }

func (m *minimalBaseModule) Commands() map[string]CommandHandler {
	return map[string]CommandHandler{
		"minimal": func(msg *Message) error { return nil },
	}
}

// customInitBaseModule declares its own Init, which shadows the promoted one.
// Base fields must still be populated when that Init runs.
type customInitBaseModule struct {
	Base
	sawClient bool
	sawDB     bool
	sawTrans  bool
}

func (m *customInitBaseModule) Name() string { return "CustomInit" }

func (m *customInitBaseModule) Commands() map[string]CommandHandler {
	return map[string]CommandHandler{"custominit": func(msg *Message) error { return nil }}
}

func (m *customInitBaseModule) Init(client *CustomTelegramClient, db *Database) error {
	m.sawClient = m.Client != nil
	m.sawDB = m.DB != nil
	m.sawTrans = m.Translator != nil
	return nil
}

// A module embedding Base satisfies Module without writing any lifecycle code.
func TestBaseSatisfiesModuleInterface(t *testing.T) {
	var _ Module = (*minimalBaseModule)(nil)
	var _ Module = (*customInitBaseModule)(nil)
}

func TestBasePopulatesFieldsOnRegistration(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)

	mod := &minimalBaseModule{}
	if err := modules.RegisterModule(mod); err != nil {
		t.Fatalf("register: %v", err)
	}

	if mod.Client == nil {
		t.Error("Base.Client not populated")
	}
	if mod.DB == nil {
		t.Error("Base.DB not populated")
	}
	if mod.Translator == nil {
		t.Error("Base.Translator not populated")
	}
	if mod.self == nil {
		t.Error("Base.self not bound; T() would not resolve the module name")
	}
}

// A module with its own Init must find Base already wired, so it never has to
// call back into Base to get a usable client.
func TestBasePopulatedBeforeCustomInitRuns(t *testing.T) {
	db := initializedTestDatabase(t, NewDatabase(42))
	client := NewCustomTelegramClient(42)
	modules := NewModules(client, db)

	mod := &customInitBaseModule{}
	if err := modules.RegisterModule(mod); err != nil {
		t.Fatalf("register: %v", err)
	}

	if !mod.sawClient {
		t.Error("Client was nil inside the module's own Init")
	}
	if !mod.sawDB {
		t.Error("DB was nil inside the module's own Init")
	}
	if !mod.sawTrans {
		t.Error("Translator was nil inside the module's own Init")
	}
}

// Defaults must be usable, not panic, and mean "nothing declared".
func TestBaseDefaults(t *testing.T) {
	mod := &minimalBaseModule{}

	if err := mod.ClientReady(); err != nil {
		t.Errorf("ClientReady() = %v, want nil", err)
	}
	if err := mod.OnUnload(); err != nil {
		t.Errorf("OnUnload() = %v, want nil", err)
	}
	if err := mod.OnDlmod(); err != nil {
		t.Errorf("OnDlmod() = %v, want nil", err)
	}
	if got := mod.Strings(); got != nil {
		t.Errorf("Strings() = %v, want nil", got)
	}
	if got := mod.Watchers(); got != nil {
		t.Errorf("Watchers() = %v, want nil", got)
	}
}

// T falls back to the default when there is no translator, rather than
// returning "" or panicking on an unregistered module.
func TestBaseTFallsBackToDefault(t *testing.T) {
	mod := &minimalBaseModule{}
	if got := mod.T("missing", "fallback"); got != "fallback" {
		t.Errorf("T() = %q, want %q", got, "fallback")
	}

	var nilBase *Base
	if got := nilBase.T("missing", "fallback"); got != "fallback" {
		t.Errorf("T() on nil Base = %q, want %q", got, "fallback")
	}
}

type aliasedModule struct{ Base }

func (m *aliasedModule) Name() string                        { return "APILimiter" }
func (m *aliasedModule) TranslationAliases() []string        { return []string{"api_protection"} }
func (m *aliasedModule) Commands() map[string]CommandHandler { return nil }

func TestTranslateModuleKeyUsesDefaultWithoutTranslator(t *testing.T) {
	mod := &aliasedModule{}
	if got := TranslateModuleKey(nil, mod, "k", "def"); got != "def" {
		t.Errorf("TranslateModuleKey(nil translator) = %q, want %q", got, "def")
	}
}
