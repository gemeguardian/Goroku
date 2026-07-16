//go:build linux

package modules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goroku/goroku"
)

func TestBuildAndOpenPluginWithExternalDataRoot(t *testing.T) {
	dataRoot := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(t.TempDir(), "source root")
	if err := os.Symlink(repoRoot, sourceRoot); err != nil {
		t.Fatal(err)
	}
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	sourcePath, err := runtimeModuleSourcePath("ExternalRootModule")
	if err != nil {
		t.Fatal(err)
	}
	source := `package modules

import "goroku/goroku"

type ExternalRootModule struct{}
func (*ExternalRootModule) Name() string { return "ExternalRootModule" }
func (*ExternalRootModule) Strings() map[string]string { return nil }
func (*ExternalRootModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error { return nil }
func (*ExternalRootModule) ClientReady() error { return nil }
func (*ExternalRootModule) OnUnload() error { return nil }
func (*ExternalRootModule) OnDlmod() error { return nil }
func (*ExternalRootModule) Commands() map[string]goroku.CommandHandler { return nil }
func (*ExternalRootModule) Watchers() []goroku.WatcherHandler { return nil }
`
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}

	mod, err := buildAndOpenPluginFromSource("ExternalRootModule", sourcePath)
	if err != nil {
		t.Fatalf("external-data-root plugin build failed: %v", err)
	}
	if mod.Name() != "ExternalRootModule" {
		t.Fatalf("plugin module name = %q", mod.Name())
	}
}

type presetRollbackModule struct{}

func (*presetRollbackModule) Name() string                                              { return "PresetRollback" }
func (*presetRollbackModule) Strings() map[string]string                                { return nil }
func (*presetRollbackModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error { return nil }
func (*presetRollbackModule) ClientReady() error                                        { return nil }
func (*presetRollbackModule) OnUnload() error                                           { return nil }
func (*presetRollbackModule) OnDlmod() error                                            { return nil }
func (*presetRollbackModule) Commands() map[string]goroku.CommandHandler                { return nil }
func (*presetRollbackModule) Watchers() []goroku.WatcherHandler                         { return nil }

func TestPresetFailedUpdatePreservesSourceRegistryAndDatabase(t *testing.T) {
	dataRoot := t.TempDir()
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	db := goroku.NewDatabase(77)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := &goroku.CustomTelegramClient{}
	client.Loader = goroku.NewModules(client, db)
	oldModule := &presetRollbackModule{}
	if err := client.Loader.RegisterModule(oldModule); err != nil {
		t.Fatal(err)
	}
	oldURL := "https://example.test/old.go"
	if err := db.SetStringMap("Loader", "loaded_modules", map[string]string{"PresetRollback": oldURL}); err != nil {
		t.Fatal("failed to seed loaded_modules")
	}
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	sourcePath, err := runtimeModuleSourcePath("PresetRollback")
	if err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n\ntype PresetRollback struct{}\n")
	if err := os.WriteFile(sourcePath, oldSource, 0600); err != nil {
		t.Fatal(err)
	}

	presets := &Presets{client: client, db: db}
	err = presets.installDownloadedModule(nil, "PresetRollback", "https://example.test/new.go", []byte("not go source"))
	if err == nil {
		t.Fatal("invalid preset update succeeded")
	}
	if got, err := os.ReadFile(sourcePath); err != nil || string(got) != string(oldSource) {
		t.Fatalf("installed source changed: %q, %v", got, err)
	}
	if got := client.Loader.LookupByName("PresetRollback"); got != oldModule {
		t.Fatalf("registered module changed: %#v", got)
	}
	if got := db.GetStringMap("Loader", "loaded_modules", nil)["PresetRollback"]; got != oldURL {
		t.Fatalf("loaded_modules changed: %q", got)
	}
}

type directRollbackModule struct{ name string }

func (m *directRollbackModule) Name() string             { return m.name }
func (*directRollbackModule) Strings() map[string]string { return nil }
func (*directRollbackModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error {
	return nil
}
func (*directRollbackModule) ClientReady() error                         { return nil }
func (*directRollbackModule) OnUnload() error                            { return nil }
func (*directRollbackModule) OnDlmod() error                             { return nil }
func (*directRollbackModule) Commands() map[string]goroku.CommandHandler { return nil }
func (*directRollbackModule) Watchers() []goroku.WatcherHandler          { return nil }

func directModuleSource(typeName string) []byte {
	return []byte(fmt.Sprintf(`package modules

import "goroku/goroku"

type %s struct{}
func (*%s) Name() string { return %q }
func (*%s) Strings() map[string]string { return nil }
func (*%s) Init(*goroku.CustomTelegramClient, *goroku.Database) error { return nil }
func (*%s) ClientReady() error { return nil }
func (*%s) OnUnload() error { return nil }
func (*%s) OnDlmod() error { return nil }
func (*%s) Commands() map[string]goroku.CommandHandler { return nil }
func (*%s) Watchers() []goroku.WatcherHandler { return nil }
`, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName))
}

func TestDirectInstallPersistenceFailureRollsBackLiveModuleAndSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance string
	}{
		{name: "dlmod", provenance: "https://example.test/direct.go"},
		{name: "loadmod", provenance: "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot := t.TempDir()
			sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
			if err != nil {
				t.Fatal(err)
			}
			oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
			goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
			t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

			db := goroku.NewDatabase(901)
			if err := db.Init(""); err != nil {
				t.Fatal(err)
			}
			client := &goroku.CustomTelegramClient{}
			client.Loader = goroku.NewModules(client, db)
			moduleName := "Direct" + tc.name
			oldModule := &directRollbackModule{name: moduleName}
			if err := client.Loader.RegisterModule(oldModule); err != nil {
				t.Fatal(err)
			}
			oldProvenance := "https://example.test/old.go"
			if err := db.SetStringMap("Loader", "loaded_modules", map[string]string{moduleName: oldProvenance}); err != nil {
				t.Fatal("failed to seed loaded_modules")
			}
			if err := ensureRuntimeModuleSourceDir(); err != nil {
				t.Fatal(err)
			}
			sourcePath, err := runtimeModuleSourcePath(moduleName)
			if err != nil {
				t.Fatal(err)
			}
			oldSource := []byte("package modules\n// previous source\n")
			if err := os.WriteFile(sourcePath, oldSource, 0600); err != nil {
				t.Fatal(err)
			}

			configPath := filepath.Join(dataRoot, "config-901.json")
			if err := os.Remove(configPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(configPath, 0700); err != nil {
				t.Fatal(err)
			}
			loader := &LoaderModule{client: client, db: db}
			loader.installHotModuleApply = func(_ *goroku.Message, fallbackName, destination string, body []byte) error {
				if current := client.Loader.LookupByName(fallbackName); current != nil {
					if err := client.Loader.UnloadModule(current.Name()); err != nil {
						return err
					}
				}
				if err := os.WriteFile(destination, body, 0600); err != nil {
					return err
				}
				return client.Loader.RegisterModule(&directRollbackModule{name: fallbackName})
			}
			err = loader.installPersistedHotModule(nil, moduleName, sourcePath, tc.provenance, directModuleSource(moduleName))
			if err == nil || !strings.Contains(err.Error(), "database update failed") {
				t.Fatalf("install error = %v, want manifest persistence failure", err)
			}
			if !errors.Is(err, goroku.ErrDatabasePersistence) {
				t.Fatalf("install error lost database cause: %v", err)
			}
			if got := client.Loader.LookupByName(moduleName); got != oldModule {
				t.Fatalf("registered module changed: %#v", got)
			}
			if got, err := os.ReadFile(sourcePath); err != nil || string(got) != string(oldSource) {
				t.Fatalf("source changed: %q, %v", got, err)
			}
			if got := db.GetStringMap("Loader", "loaded_modules", nil)[moduleName]; got != oldProvenance {
				t.Fatalf("manifest provenance = %q", got)
			}
		})
	}
}

func TestRestoreAndInstallSerializeRuntimeModuleTransaction(t *testing.T) {
	dataRoot := t.TempDir()
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	db := goroku.NewDatabase(902)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := &goroku.CustomTelegramClient{}
	client.Loader = goroku.NewModules(client, db)
	restoreEntered := make(chan struct{})
	releaseRestore := make(chan struct{})
	backup := &GorokuBackup{db: db, compileModuleValidation: func(string, []byte) error { return nil }, restoreApplyFile: func(source, destination string) error {
		close(restoreEntered)
		<-releaseRestore
		return os.Rename(source, destination)
	}}
	restoreSource := directModuleSource("ConcurrentRestore")
	mods := makeZip(t, map[string][]byte{
		"db_mods.json":         []byte(`{"ConcurrentRestore":"local"}`),
		"ConcurrentRestore.go": restoreSource,
	})
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- backup.restoreModulesFromData(mods) }()
	select {
	case <-restoreEntered:
	case err := <-restoreDone:
		t.Fatalf("restore failed before apply: %v", err)
	case <-time.After(2 * time.Minute):
		t.Fatal("restore did not reach its apply transaction")
	}

	installSource := append(directModuleSource("ConcurrentRestore"), []byte("\n// installed after restore\n")...)
	installDone := make(chan error, 1)
	go func() {
		path, pathErr := runtimeModuleSourcePath("ConcurrentRestore")
		if pathErr != nil {
			installDone <- pathErr
			return
		}
		loader := &LoaderModule{client: client, db: db}
		loader.installHotModuleApply = func(_ *goroku.Message, fallbackName, destination string, body []byte) error {
			if err := os.WriteFile(destination, body, 0600); err != nil {
				return err
			}
			return client.Loader.RegisterModule(&directRollbackModule{name: fallbackName})
		}
		installDone <- loader.installPersistedHotModule(nil, "ConcurrentRestore", path, "https://example.test/current.go", installSource)
	}()
	select {
	case err := <-installDone:
		t.Fatalf("install completed before restore released the transaction lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseRestore)
	if err := <-restoreDone; err != nil {
		t.Fatal(err)
	}
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	path, err := runtimeModuleSourcePath("ConcurrentRestore")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(installSource) {
		t.Fatalf("final source = %q, %v", got, err)
	}
	if got := db.GetStringMap("Loader", "loaded_modules", nil)["ConcurrentRestore"]; got != "https://example.test/current.go" {
		t.Fatalf("final provenance = %q", got)
	}
}

func committedManifestWarning(cause error) error {
	return &goroku.DatabaseError{
		Operation: "set",
		Owner:     "Loader",
		Key:       "loaded_modules",
		Backend:   "local",
		Committed: true,
		Err:       errors.Join(goroku.ErrDatabasePersistence, goroku.ErrDatabaseCommitUncertain, cause),
	}
}

func newModuleTransactionHarness(t *testing.T, id int64) (*goroku.Database, *goroku.CustomTelegramClient, string) {
	t.Helper()
	dataRoot := t.TempDir()
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })
	db := goroku.NewDatabase(id)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	client := &goroku.CustomTelegramClient{}
	client.Loader = goroku.NewModules(client, db)
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	return db, client, dataRoot
}

func testInstallApply(client *goroku.CustomTelegramClient) func(*goroku.Message, string, string, []byte) error {
	return func(_ *goroku.Message, name, destination string, body []byte) error {
		if old := client.Loader.LookupByName(name); old != nil {
			if _, err := unloadModuleForTransaction(client.Loader, old.Name()); err != nil {
				return err
			}
		}
		if err := os.WriteFile(destination, body, 0600); err != nil {
			return err
		}
		return client.Loader.RegisterModule(&directRollbackModule{name: name})
	}
}

func TestInstallCommittedManifestWarningRetainsSourceAndRuntime(t *testing.T) {
	db, client, _ := newModuleTransactionHarness(t, 903)
	name := "CommittedInstall"
	path, err := runtimeModuleSourcePath(name)
	if err != nil {
		t.Fatal(err)
	}
	body := directModuleSource(name)
	cause := errors.New("injected post-rename sync failure")
	loader := &LoaderModule{
		client:                client,
		db:                    db,
		installHotModuleApply: testInstallApply(client),
		setLoadedModulesApply: func(modules map[string]string) error {
			if err := db.SetStringMap("Loader", "loaded_modules", modules); err != nil {
				return err
			}
			return committedManifestWarning(cause)
		},
	}
	err = loader.installPersistedHotModule(nil, name, path, "local", body)
	if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) || !errors.Is(err, cause) {
		t.Fatalf("install error = %v, want committed durability warning and cause", err)
	}
	var diagnostic *goroku.DatabaseError
	if !errors.As(err, &diagnostic) || !diagnostic.Committed {
		t.Fatalf("install diagnostic = %#v", diagnostic)
	}
	if got := client.Loader.LookupByName(name); got == nil {
		t.Fatal("committed install rolled back runtime module")
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(body) {
		t.Fatalf("committed install source = %q, %v", got, readErr)
	}
	if got := db.GetStringMap("Loader", "loaded_modules", nil)[name]; got != "local" {
		t.Fatalf("committed manifest provenance = %q", got)
	}
}

func TestUninstallManifestFaultSemantics(t *testing.T) {
	for i, tc := range []struct {
		name      string
		committed bool
	}{
		{name: "ordinary failure"},
		{name: "post-rename committed warning", committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, client, _ := newModuleTransactionHarness(t, int64(910+i))
			name := fmt.Sprintf("UninstallFault%d", i)
			old := &directRollbackModule{name: name}
			if err := client.Loader.RegisterModule(old); err != nil {
				t.Fatal(err)
			}
			if err := db.SetStringMap("Loader", "loaded_modules", map[string]string{name: "local"}); err != nil {
				t.Fatal(err)
			}
			path, err := runtimeModuleSourcePath(name)
			if err != nil {
				t.Fatal(err)
			}
			body := directModuleSource(name)
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("injected manifest persistence failure")
			loader := &LoaderModule{client: client, db: db}
			loader.setLoadedModulesApply = func(modules map[string]string) error {
				if tc.committed {
					if err := db.SetStringMap("Loader", "loaded_modules", modules); err != nil {
						return err
					}
					return committedManifestWarning(cause)
				}
				return errors.Join(goroku.ErrDatabasePersistence, cause)
			}
			err = loader.uninstallPersistedHotModule(nil, name, name)
			if !errors.Is(err, cause) {
				t.Fatalf("uninstall error lost cause: %v", err)
			}
			if got := errors.Is(err, goroku.ErrDatabaseCommitUncertain); got != tc.committed {
				t.Fatalf("committed warning classification = %v, want %v: %v", got, tc.committed, err)
			}
			if tc.committed {
				if client.Loader.LookupByName(name) != nil {
					t.Fatal("committed uninstall restored runtime module")
				}
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("committed uninstall source still exists: %v", statErr)
				}
				if _, exists := db.GetStringMap("Loader", "loaded_modules", nil)[name]; exists {
					t.Fatal("committed uninstall manifest entry still exists")
				}
				return
			}
			if got := client.Loader.LookupByName(name); got != old {
				t.Fatalf("ordinary failure did not restore runtime module: %#v", got)
			}
			if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(body) {
				t.Fatalf("ordinary failure source = %q, %v", got, readErr)
			}
			if got := db.GetStringMap("Loader", "loaded_modules", nil)[name]; got != "local" {
				t.Fatalf("ordinary failure manifest provenance = %q", got)
			}
		})
	}
}

func TestPresetManifestFaultSemantics(t *testing.T) {
	for i, tc := range []struct {
		name      string
		committed bool
	}{
		{name: "ordinary failure"},
		{name: "post-rename committed warning", committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, client, _ := newModuleTransactionHarness(t, int64(920+i))
			name := fmt.Sprintf("PresetFault%d", i)
			path, err := runtimeModuleSourcePath(name)
			if err != nil {
				t.Fatal(err)
			}
			body := directModuleSource(name)
			cause := errors.New("injected preset manifest failure")
			presets := &Presets{
				client:                client,
				db:                    db,
				installHotModuleApply: testInstallApply(client),
				setLoadedModulesApply: func(modules map[string]string) error {
					if tc.committed {
						if err := db.SetStringMap("Loader", "loaded_modules", modules); err != nil {
							return err
						}
						return committedManifestWarning(cause)
					}
					return errors.Join(goroku.ErrDatabasePersistence, cause)
				},
			}
			err = presets.installDownloadedModule(nil, name, "https://example.test/preset.go", body)
			if !errors.Is(err, cause) {
				t.Fatalf("preset error lost cause: %v", err)
			}
			if got := errors.Is(err, goroku.ErrDatabaseCommitUncertain); got != tc.committed {
				t.Fatalf("preset committed classification = %v, want %v", got, tc.committed)
			}
			if tc.committed {
				if client.Loader.LookupByName(name) == nil {
					t.Fatal("committed preset install rolled back runtime module")
				}
				if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(body) {
					t.Fatalf("committed preset source = %q, %v", got, readErr)
				}
				return
			}
			if client.Loader.LookupByName(name) != nil {
				t.Fatal("ordinary preset failure retained runtime module")
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("ordinary preset failure retained source: %v", statErr)
			}
		})
	}
}

type teardownOrderingModule struct {
	name      string
	events    chan<- string
	command   goroku.CommandHandler
	unloadErr error
}

func (m *teardownOrderingModule) Name() string             { return m.name }
func (*teardownOrderingModule) Strings() map[string]string { return nil }
func (m *teardownOrderingModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error {
	if m.events != nil {
		m.events <- "new-init"
	}
	return nil
}
func (m *teardownOrderingModule) ClientReady() error { m.events <- "new-ready"; return nil }
func (m *teardownOrderingModule) OnUnload() error {
	if m.events != nil {
		m.events <- "old-unload"
	}
	return m.unloadErr
}
func (m *teardownOrderingModule) OnDlmod() error { m.events <- "new-dlmod"; return nil }
func (m *teardownOrderingModule) Commands() map[string]goroku.CommandHandler {
	if m.command == nil {
		return nil
	}
	return map[string]goroku.CommandHandler{"ordering": m.command}
}
func (*teardownOrderingModule) Watchers() []goroku.WatcherHandler { return nil }

func TestLeasedHandlerSelfReplaceIsRejectedCoherently(t *testing.T) {
	db, client, _ := newModuleTransactionHarness(t, 928)
	name := "SelfReplace"
	path, err := runtimeModuleSourcePath(name)
	if err != nil {
		t.Fatal(err)
	}
	oldSource := append(directModuleSource(name), []byte("\n// old source\n")...)
	if err := os.WriteFile(path, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	oldProvenance := "https://example.test/old.go"
	if err := db.SetStringMap("Loader", "loaded_modules", map[string]string{name: oldProvenance}); err != nil {
		t.Fatal(err)
	}

	loader := &LoaderModule{client: client, db: db}
	applyCalled := false
	loader.installHotModuleApply = func(*goroku.Message, string, string, []byte) error {
		applyCalled = true
		return errors.New("self-replacement reached apply")
	}
	old := &teardownOrderingModule{name: name}
	old.command = func(msg *goroku.Message) error {
		return loader.installPersistedHotModule(msg, name, path, "local", directModuleSource(name))
	}
	if err := client.Loader.RegisterModule(old); err != nil {
		t.Fatal(err)
	}
	handler, ok := client.Loader.Dispatch("ordering")
	if !ok {
		t.Fatal("self-replace handler not registered")
	}
	done := make(chan error, 1)
	go func() { done <- handler(&goroku.Message{Client: client}) }()
	select {
	case err := <-done:
		var rejected *SelfModuleTransactionError
		if !errors.As(err, &rejected) || rejected.Action != "replace" || rejected.Module != name {
			t.Fatalf("self-replace error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("self-replace deadlocked while holding its module lease")
	}
	if applyCalled {
		t.Fatal("self-replace mutated runtime before rejection")
	}
	if got := client.Loader.LookupByName(name); got != old {
		t.Fatalf("runtime module changed: %#v", got)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(oldSource) {
		t.Fatalf("source changed: %q, %v", got, readErr)
	}
	if got := db.GetStringMap("Loader", "loaded_modules", nil)[name]; got != oldProvenance {
		t.Fatalf("manifest provenance changed: %q", got)
	}
}

func TestLeasedHandlerSelfUninstallIsRejectedCoherently(t *testing.T) {
	db, client, _ := newModuleTransactionHarness(t, 929)
	name := "SelfUninstall"
	path, err := runtimeModuleSourcePath(name)
	if err != nil {
		t.Fatal(err)
	}
	oldSource := directModuleSource(name)
	if err := os.WriteFile(path, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	oldProvenance := "local"
	if err := db.SetStringMap("Loader", "loaded_modules", map[string]string{name: oldProvenance}); err != nil {
		t.Fatal(err)
	}

	loader := &LoaderModule{client: client, db: db}
	old := &teardownOrderingModule{name: name}
	old.command = func(msg *goroku.Message) error {
		return loader.uninstallPersistedHotModule(msg, name, name)
	}
	if err := client.Loader.RegisterModule(old); err != nil {
		t.Fatal(err)
	}
	handler, ok := client.Loader.Dispatch("ordering")
	if !ok {
		t.Fatal("self-uninstall handler not registered")
	}
	done := make(chan error, 1)
	go func() { done <- handler(&goroku.Message{Client: client}) }()
	select {
	case err := <-done:
		var rejected *SelfModuleTransactionError
		if !errors.As(err, &rejected) || rejected.Action != "uninstall" || rejected.Module != name {
			t.Fatalf("self-uninstall error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("self-uninstall deadlocked while holding its module lease")
	}
	if got := client.Loader.LookupByName(name); got != old {
		t.Fatalf("runtime module changed: %#v", got)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(oldSource) {
		t.Fatalf("source changed: %q, %v", got, readErr)
	}
	if got := db.GetStringMap("Loader", "loaded_modules", nil)[name]; got != oldProvenance {
		t.Fatalf("manifest provenance changed: %q", got)
	}
}

func TestReplacementWaitsForDeferredTeardown(t *testing.T) {
	_, client, _ := newModuleTransactionHarness(t, 930)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	events := make(chan string, 4)
	old := &teardownOrderingModule{name: "Ordering", command: func(*goroku.Message) error {
		close(handlerStarted)
		<-releaseHandler
		return nil
	}}
	if err := client.Loader.RegisterModule(old); err != nil {
		t.Fatal(err)
	}
	old.events = events
	handler, ok := client.Loader.Dispatch("ordering")
	if !ok {
		t.Fatal("ordering handler not registered")
	}
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- handler(nil) }()
	<-handlerStarted

	replacement := &teardownOrderingModule{name: "Ordering", events: events}
	replaceDone := make(chan error, 1)
	go func() { replaceDone <- replaceOpenedPlugin(client.Loader, replacement) }()
	select {
	case event := <-events:
		t.Fatalf("lifecycle overlapped active old handler: %s", event)
	case err := <-replaceDone:
		t.Fatalf("replacement completed before old handler drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHandler)
	if err := <-handlerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-replaceDone; err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"old-unload", "new-init", "new-dlmod", "new-ready"} {
		if got := <-events; got != want {
			t.Fatalf("lifecycle event %d = %q, want %q", i, got, want)
		}
	}
}

func TestReplacementRestoresModuleAfterDeferredTeardownError(t *testing.T) {
	_, client, _ := newModuleTransactionHarness(t, 931)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	cause := errors.New("injected deferred teardown failure")
	old := &teardownOrderingModule{
		name:      "OrderingFailure",
		unloadErr: cause,
		command: func(*goroku.Message) error {
			close(handlerStarted)
			<-releaseHandler
			return nil
		},
	}
	if err := client.Loader.RegisterModule(old); err != nil {
		t.Fatal(err)
	}
	handler, ok := client.Loader.Dispatch("ordering")
	if !ok {
		t.Fatal("ordering handler not registered")
	}
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- handler(nil) }()
	<-handlerStarted
	replaceDone := make(chan error, 1)
	go func() {
		replaceDone <- replaceOpenedPlugin(client.Loader, &teardownOrderingModule{
			name:   "OrderingFailure",
			events: make(chan string, 2),
		})
	}()
	select {
	case err := <-replaceDone:
		t.Fatalf("replacement completed before deferred teardown: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHandler)
	if err := <-handlerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-replaceDone; !errors.Is(err, cause) {
		t.Fatalf("replacement error = %v, want teardown cause", err)
	}
	if got := client.Loader.LookupByName(old.Name()); got != old {
		t.Fatalf("detached module was not restored: %#v", got)
	}
}
