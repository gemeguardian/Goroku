package modules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goroku/goroku"
)

type persistenceTestModule struct {
	name         string
	canonicalKey string

	mu          sync.Mutex
	configReady int
	activeReady int
	concurrent  bool
	lastValue   any
}

func (m *persistenceTestModule) Name() string               { return m.name }
func (m *persistenceTestModule) Strings() map[string]string { return map[string]string{} }
func (m *persistenceTestModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error {
	return nil
}
func (m *persistenceTestModule) ClientReady() error { return nil }
func (m *persistenceTestModule) OnUnload() error    { return nil }
func (m *persistenceTestModule) OnDlmod() error     { return nil }
func (m *persistenceTestModule) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{"run": func(*goroku.Message) error { return nil }}
}
func (m *persistenceTestModule) Watchers() []goroku.WatcherHandler { return nil }
func (m *persistenceTestModule) ConfigDefaults() map[string]any {
	return map[string]any{m.configKey(): "default"}
}

func (m *persistenceTestModule) configKey() string {
	if m.canonicalKey != "" {
		return m.canonicalKey
	}
	return "value"
}

func (m *persistenceTestModule) ConfigReady(config map[string]any) error {
	m.mu.Lock()
	m.configReady++
	m.activeReady++
	if m.activeReady > 1 {
		m.concurrent = true
	}
	m.lastValue = config[m.configKey()]
	m.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	m.mu.Lock()
	m.activeReady--
	m.mu.Unlock()
	return nil
}

func (m *persistenceTestModule) resetReadyState() {
	m.mu.Lock()
	m.configReady = 0
	m.concurrent = false
	m.lastValue = nil
	m.mu.Unlock()
}

func (m *persistenceTestModule) readyState() (int, bool, any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configReady, m.concurrent, m.lastValue
}

func newFailingModuleTest(t *testing.T) (*goroku.CustomTelegramClient, *goroku.Database) {
	t.Helper()
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	db := goroku.NewDatabase(1001)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(goroku.BaseDir, "config-1001.json")
	if err := db.Update(map[string]map[string]any{
		"ConfigTarget":  {"value": "default"},
		"RuntimeTarget": {"value": "default"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configPath, 0700); err != nil {
		t.Fatal(err)
	}
	client := goroku.NewCustomTelegramClient(1001)
	client.GorokuDB = db
	client.Loader = goroku.NewModules(client, db)
	return client, db
}

func requirePersistenceFailure(t *testing.T, err error, text string) {
	t.Helper()
	if !errors.Is(err, goroku.ErrDatabasePersistence) {
		t.Fatalf("error = %v, want wrapped database persistence error", err)
	}
	var databaseErr *goroku.DatabaseError
	if !errors.As(err, &databaseErr) {
		t.Fatalf("error = %v, want DatabaseError", err)
	}
	if strings.Contains(strings.ToLower(text), "saved") || strings.Contains(text, "success") {
		t.Fatalf("failure reported success: %q", text)
	}
}

func TestConfigWriteFailureDoesNotReloadOrReportSuccess(t *testing.T) {
	client, db := newFailingModuleTest(t)
	target := &persistenceTestModule{name: "ConfigTarget"}
	if err := client.Loader.RegisterModule(target); err != nil {
		t.Fatal(err)
	}
	target.resetReadyState()

	module := &GorokuConfig{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{RawText: ".fcfg ConfigTarget value changed", Text: ".fcfg ConfigTarget value changed", Client: client}
	err := module.FConfigCmd(msg)

	requirePersistenceFailure(t, err, msg.Text)
	if calls, _, _ := target.readyState(); calls != 0 {
		t.Fatalf("ConfigReady called %d times after failed write", calls)
	}
}

func TestMixedCaseConfigWriteReloadsExactlyOnce(t *testing.T) {
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	db := goroku.NewDatabase(1002)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := goroku.NewCustomTelegramClient(1002)
	client.GorokuDB = db
	client.Loader = goroku.NewModules(client, db)
	target := &persistenceTestModule{name: "ConfigTarget", canonicalKey: "Value"}
	if err := client.Loader.RegisterModule(target); err != nil {
		t.Fatal(err)
	}
	target.resetReadyState()

	module := &GorokuConfig{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{RawText: ".fcfg cOnFiGtArGeT VaLuE changed", Text: ".fcfg cOnFiGtArGeT VaLuE changed", Client: client}
	if err := module.FConfigCmd(msg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	calls, concurrent, loadedValue := target.readyState()
	if calls != 1 {
		t.Fatalf("ConfigReady calls = %d, want exactly 1", calls)
	}
	if concurrent {
		t.Fatal("ConfigReady invocations overlapped")
	}
	if loadedValue != "changed" {
		t.Fatalf("loader consumed value %v, want changed", loadedValue)
	}
	data := db.GetAll()[target.Name()]
	if data["Value"] != "changed" {
		t.Fatalf("canonical value = %v, want changed", data["Value"])
	}
	if _, exists := data["VaLuE"]; exists {
		t.Fatal("mixed-case shadow key was persisted")
	}
}

func TestMixedCaseConfigResetDeletesCanonicalKey(t *testing.T) {
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	db := goroku.NewDatabase(1003)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := goroku.NewCustomTelegramClient(1003)
	client.GorokuDB = db
	client.Loader = goroku.NewModules(client, db)
	target := &persistenceTestModule{name: "ConfigTarget", canonicalKey: "Value"}
	if err := client.Loader.RegisterModule(target); err != nil {
		t.Fatal(err)
	}
	if err := db.Set(target.Name(), "Value", "changed"); err != nil {
		t.Fatal(err)
	}
	target.resetReadyState()

	module := &GorokuConfig{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{RawText: ".dcfg CONFIGTARGET VALUE", Text: ".dcfg CONFIGTARGET VALUE", Client: client}
	if err := module.DConfigCmd(msg); err != nil {
		t.Fatal(err)
	}

	data := db.GetAll()[target.Name()]
	if data["Value"] != "default" {
		t.Fatalf("canonical value after reset = %v, want default", data["Value"])
	}
	if _, exists := data["VALUE"]; exists {
		t.Fatal("mixed-case shadow key was persisted")
	}
	calls, concurrent, loadedValue := target.readyState()
	if calls != 1 || concurrent {
		t.Fatalf("ConfigReady state = (%d, concurrent=%v), want one non-concurrent call", calls, concurrent)
	}
	if loadedValue != "default" {
		t.Fatalf("loader consumed value %v after reset, want default", loadedValue)
	}
}

func TestSettingsWriteFailureDoesNotMutateRuntimeRegistry(t *testing.T) {
	client, db := newFailingModuleTest(t)
	target := &persistenceTestModule{name: "RuntimeTarget"}
	if err := client.Loader.RegisterModule(target); err != nil {
		t.Fatal(err)
	}
	module := &SettingsModule{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{Text: ".togglemod RuntimeTarget", Client: client}
	err := module.ToggleModCmd(msg)

	requirePersistenceFailure(t, err, msg.Text)
	if _, enabled := client.Loader.Dispatch("run"); !enabled {
		t.Fatal("runtime command was disabled after persistence failure")
	}
}

func TestTranslationWriteFailureDoesNotReportSuccess(t *testing.T) {
	client, db := newFailingModuleTest(t)
	module := &TranslationsModule{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{Text: ".setlang ru", Client: client}
	err := module.SetLangCmd(msg)

	requirePersistenceFailure(t, err, msg.Text)
}

func TestInlineWriteFailureDoesNotReportSuccess(t *testing.T) {
	client, db := newFailingModuleTest(t)
	module := &InlineStuff{}
	if err := module.Init(client, db); err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{
		Text:   ".ch_bot_token 12345678:abcdefghijklmnopqrstuvwxyzABCDEFGH",
		Client: client,
	}
	err := module.ChBotTokenCmd(msg)

	requirePersistenceFailure(t, err, msg.Text)
}
