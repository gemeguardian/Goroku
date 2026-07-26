package modules

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goroku/goroku"
)

func TestGorokuBackupLoadedModulesMap(t *testing.T) {
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader": {
			"loaded_modules": map[string]any{"example": "https://example.test/example.go"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	m := newBackupTestModule(db)
	got, err := m.loadedModulesMapChecked()
	if err != nil {
		t.Fatal(err)
	}
	if got["example"] != "https://example.test/example.go" {
		t.Fatalf("loadedModulesMapChecked() = %#v", got)
	}
}

func TestRestoreAllFromZipValidBackup(t *testing.T) {
	setModuleTestRoots(t)
	db := goroku.NewDatabase(1)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"example":"https://example.test/example.go"}`),
		"example.go":   testModuleSource("Example", "valid restore"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"!"},"goroku.inline":{"bot_token":"secret"}}`),
		"mods.zip": mods,
	})

	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatalf("restoreAllFromZip() error = %v", err)
	}
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "!" {
		t.Fatalf("restored command prefix = %q", got)
	}
	if got := db.GetString("goroku.inline", "bot_token", ""); got != "" {
		t.Fatalf("bot token was restored: %q", got)
	}
	loaded, err := m.loadedModulesMapChecked()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded["example"]; got != "https://example.test/example.go" {
		t.Fatalf("restored module URL = %q", got)
	}
}

func TestRestoreComponentPayloadsFromFullBackup(t *testing.T) {
	dbJSON := []byte(`{"goroku.main":{"command_prefix":"!"}}`)
	modsZIP := makeZip(t, map[string][]byte{"db_mods.json": []byte(`{}`)})
	backup := makeZip(t, map[string][]byte{
		"db.json":  dbJSON,
		"mods.zip": modsZIP,
	})

	gotDB, err := restoreDatabasePayload(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDB, dbJSON) {
		t.Fatalf("database payload = %q, want %q", gotDB, dbJSON)
	}

	gotMods, err := restoreModulesPayload(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotMods, modsZIP) {
		t.Fatal("module payload does not match nested mods.zip")
	}
}

func TestRestoreComponentPayloadsAcceptStandaloneBackups(t *testing.T) {
	dbJSON := []byte(`{"goroku.main":{"command_prefix":"!"}}`)
	gotDB, err := restoreDatabasePayload(dbJSON)
	if err != nil || !bytes.Equal(gotDB, dbJSON) {
		t.Fatalf("standalone database payload = %q, %v", gotDB, err)
	}

	modsZIP := makeZip(t, map[string][]byte{"db_mods.json": []byte(`{}`)})
	gotMods, err := restoreModulesPayload(modsZIP)
	if err != nil || !bytes.Equal(gotMods, modsZIP) {
		t.Fatalf("standalone module payload changed: %v", err)
	}
}

func TestRestoreDatabasePayloadRejectsModulesArchive(t *testing.T) {
	modsZIP := makeZip(t, map[string][]byte{"db_mods.json": []byte(`{}`)})
	if _, err := restoreDatabasePayload(modsZIP); err == nil || !strings.Contains(err.Error(), "db.json") {
		t.Fatalf("restoreDatabasePayload() error = %v, want missing db.json", err)
	}
}

func TestRestoreModulesPayloadIdentifiesDatabaseBackup(t *testing.T) {
	dbJSON := []byte(`{"Help":{"banner_url":""},"Loader":{"loaded_modules":{}}}`)
	if _, err := restoreModulesPayload(dbJSON); err == nil || !strings.Contains(err.Error(), ".restoredb") {
		t.Fatalf("restoreModulesPayload() error = %v, want database-only guidance", err)
	}
}

func TestBackupAndRestoreModulesUseRuntimeDataRoot(t *testing.T) {
	dataRoot, sourceRoot := setModuleTestRoots(t)
	legacyDir := filepath.Join(sourceRoot, "goroku", "modules")
	if err := os.MkdirAll(legacyDir, 0750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "example.go")
	legacyBody := []byte("package modules\n// legacy user source\n")
	if err := os.WriteFile(legacyPath, legacyBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	runtimePath, err := runtimeModuleSourcePath("example")
	if err != nil {
		t.Fatal(err)
	}
	runtimeBody := testModuleSource("Example", "runtime source")
	if err := os.WriteFile(runtimePath, runtimeBody, 0600); err != nil {
		t.Fatal(err)
	}

	db := goroku.NewDatabase(1)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"example": "local"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	archive, err := m.buildArchive()
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleBodyFromBackup(t, archive, "example.go"); !bytes.Equal(got, runtimeBody) {
		t.Fatalf("backed up module = %q, want runtime source", got)
	}

	if err := os.Remove(runtimePath); err != nil {
		t.Fatal(err)
	}
	if err := m.restoreAllFromZip(archive); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dataRoot, "modules", "example.go")); err != nil || !bytes.Equal(got, runtimeBody) {
		t.Fatalf("restored runtime module = %q, %v", got, err)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, legacyBody) {
		t.Fatalf("legacy source changed: %q, %v", got, err)
	}
}

func TestRestoreLimitsFileCount(t *testing.T) {
	files := make(map[string][]byte, maxRestoreFiles+1)
	files["db.json"] = []byte(`{}`)
	for i := 0; i <= maxRestoreFiles; i++ {
		files[fmt.Sprintf("file-%03d", i)] = nil
	}
	m := newBackupTestModule(goroku.NewDatabase(1))

	err := m.restoreAllFromZip(makeZip(t, files))
	if err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("account() error = %v, want file-count error", err)
	}
}

func TestRestoreLimitsTotalUncompressedBytes(t *testing.T) {
	limits := &restoreLimits{totalBytes: maxRestoreUncompressedBytes}
	zr := openZip(t, makeZip(t, map[string][]byte{"extra": {1}}))

	err := limits.account(zr.File, false)
	if err == nil || !strings.Contains(err.Error(), "uncompressed bytes") {
		t.Fatalf("account() error = %v, want total-size error", err)
	}
}

func TestRestoreLimitsNestedContent(t *testing.T) {
	limits := &restoreLimits{nestedBytes: maxRestoreNestedBytes}
	zr := openZip(t, makeZip(t, map[string][]byte{"module.go": {1}}))

	err := limits.account(zr.File, true)
	if err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("account() error = %v, want nested-size error", err)
	}
}

func TestRestoreAllInvalidNestedManifestDoesNotChangeLiveState(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(dataRoot, "modules", "old.go")
	oldSource := []byte("package modules\n")
	if err := os.WriteFile(modulePath, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := goroku.NewDatabase(1)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"broken":`),
		"new.go":       []byte("package modules\n"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})

	if err := m.restoreAllFromZip(backup); err == nil {
		t.Fatal("invalid nested manifest was accepted")
	}
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("database was partially restored: %q", got)
	}
	if got, err := os.ReadFile(modulePath); err != nil || !bytes.Equal(got, oldSource) {
		t.Fatalf("module storage was partially restored: %q, %v", got, err)
	}
}

func TestRestoreAllInvalidModuleSourceDoesNotChangeLiveState(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(dataRoot, "modules", "old.go")
	if err := os.WriteFile(modulePath, []byte("package modules\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db := goroku.NewDatabase(1)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"new":"local"}`),
		"new.go":       []byte("not go source"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})

	if err := m.restoreAllFromZip(backup); err == nil || !strings.Contains(err.Error(), "invalid module source") {
		t.Fatalf("restore error = %v, want module source validation error", err)
	}
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("database was partially restored: %q", got)
	}
	if _, err := os.Stat(modulePath); err != nil {
		t.Fatalf("old module was removed: %v", err)
	}
}

func TestRestorePreservesUnrelatedRuntimeModules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restore func(*GorokuBackup, []byte) error
	}{
		{name: "full", restore: func(m *GorokuBackup, mods []byte) error {
			return m.restoreAllFromZip(makeZip(t, map[string][]byte{
				"db.json":  []byte(`{"goroku.main":{"command_prefix":"!"}}`),
				"mods.zip": mods,
			}))
		}},
		{name: "modules", restore: func(m *GorokuBackup, mods []byte) error {
			return m.restoreModulesFromData(mods)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot, _ := setModuleTestRoots(t)
			modsDir := filepath.Join(dataRoot, "modules")
			if err := os.MkdirAll(modsDir, 0700); err != nil {
				t.Fatal(err)
			}
			unrelated := []byte("package modules\n// unrelated\n")
			if err := os.WriteFile(filepath.Join(modsDir, "unrelated.go"), unrelated, 0600); err != nil {
				t.Fatal(err)
			}
			db := newBackupTestDB(t)
			m := newBackupTestModule(db)
			mods := makeZip(t, map[string][]byte{
				"db_mods.json": []byte(`{"owned":"local"}`),
				"owned.go":     testModuleSource("Owned", "restored"),
			})

			if err := tc.restore(m, mods); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(modsDir, "unrelated.go")); err != nil || !bytes.Equal(got, unrelated) {
				t.Fatalf("unrelated module changed: %q, %v", got, err)
			}
		})
	}
}

func TestRestoreReducedManifestRemovesOnlyPreviouslyOwnedSource(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldRemoved := testModuleSource("Removed", "old removed")
	unrelated := []byte("package modules\n// unrelated runtime file\n")
	if err := os.WriteFile(filepath.Join(modsDir, "removed.go"), oldRemoved, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "unrelated.go"), unrelated, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"kept": "local", "removed": "local"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"kept":"local"}`),
		"kept.go":      testModuleSource("Kept", "new kept"),
	})

	if err := m.restoreModulesFromData(mods); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "removed.go")); !os.IsNotExist(err) {
		t.Fatalf("source dropped from manifest still exists: %v", err)
	}
	assertFileBody(t, filepath.Join(modsDir, "unrelated.go"), unrelated)
}

func TestRestoreJournalClearedAfterSuccessfulModulesRestore(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"kept":"local"}`),
		"kept.go":      testModuleSource("Kept", "journal success"),
	})
	if err := m.restoreModulesFromData(mods); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("restore journal left behind after success: %v", err)
	}
	assertFileBody(t, filepath.Join(modsDir, "kept.go"), testModuleSource("Kept", "journal success"))
}

func TestRestoreJournalRollsBackFilesAppliedPhase(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "partial restore")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), newBody, 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after FS apply: journal still at files_applied with previous snapshot.
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name:    "owned.go",
		Install: true,
		Existed: true,
		Applied: true,
		Mode:    0600,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": newBody}, ""); err != nil {
		t.Fatal(err)
	}
	// begin overwrote previous from live (already new); write the true pre-restore body.
	if err := writeFileDurable(filepath.Join(journal.previousDir(), "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Applied = true
	if err := journal.markFilesApplied(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after recovery: %v", err)
	}
}

func TestRestoreJournalRollsBackUnmarkedAppliedMutation(t *testing.T) {
	// Crash window: live FS already mutated, journal entry.Applied still false.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// pre-crash\n")
	newBody := testModuleSource("Owned", "mutated without journal mark")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), newBody, 0640); err != nil {
		t.Fatal(err)
	}
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name:    "owned.go",
		Install: true,
		Existed: true,
		Applied: false,
		Mode:    0640,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": newBody}, ""); err != nil {
		t.Fatal(err)
	}
	if err := writeFileDurable(filepath.Join(journal.previousDir(), "owned.go"), oldBody, 0640); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.markApplying(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
	if info, err := os.Stat(filepath.Join(modsDir, "owned.go")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("restored mode = %v, %v", info, err)
	}
}

func TestRestoreJournalDropsDBAppliedJournal(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := testModuleSource("Owned", "committed")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), body, 0600); err != nil {
		t.Fatal(err)
	}
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name:    "owned.go",
		Install: true,
		Existed: false,
		Applied: true,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": body}, ""); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Applied = true
	if err := journal.markDBApplied(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), body)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("db_applied journal not cleared: %v", err)
	}
}

func seedOwnedRestoreJournal(t *testing.T, modsDir string, newBody, oldBody []byte) (*restoreJournal, *restoreJournalState) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), newBody, 0600); err != nil {
		t.Fatal(err)
	}
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name:    "owned.go",
		Install: true,
		Existed: true,
		Applied: true,
		Mode:    0600,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": newBody}, ""); err != nil {
		t.Fatal(err)
	}
	if err := writeFileDurable(filepath.Join(journal.previousDir(), "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Applied = true
	return journal, state
}

func TestRestoreJournalRollsBackDBApplyingPhase(t *testing.T) {
	// Crash window: FS applied, journal at db_applying (DB uncertain) → prefer FS rollback.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "db applying crash")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after db_applying recovery: %v", err)
	}
}

func TestRestoreJournalRollsBackUnknownPhase(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old unknown\n")
	newBody := testModuleSource("Owned", "unknown phase")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	state.Phase = "not-a-real-phase"
	if err := journal.writeState(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after unknown phase recovery: %v", err)
	}
}

func TestRestoreJournalRollsBackEmptyPhase(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old empty phase\n")
	newBody := testModuleSource("Owned", "empty phase")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	state.Phase = ""
	if err := journal.writeState(state); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
}

func TestApplyRestoreAdvancesJournalPhasesBeforeAndAfterDB(t *testing.T) {
	// Spy: DB Reset must only run after files_applied + db_applying are durable.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// old\n")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	var phaseAtDB string
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		journal := openRestoreJournal()
		if !journal.exists() {
			t.Fatal("journal missing when Database.Reset called")
		}
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		phaseAtDB = state.Phase
		return db.Reset(data)
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "phase order"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatal(err)
	}
	if phaseAtDB != restorePhaseDBApplying {
		t.Fatalf("phase at Database.Reset = %q, want %q", phaseAtDB, restorePhaseDBApplying)
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal left after successful restore: %v", err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), testModuleSource("Owned", "phase order"))
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("command_prefix = %q, want new", got)
	}
}

func TestApplyRestoreCrashBetweenFilesAppliedAndDBRollsBackOnRecovery(t *testing.T) {
	// Simulate process death after files_applied, before/during DB: recovery rolls FS.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// pre-db crash\n")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected crash before db durability")
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		// Leave journal at db_applying and refuse DB commit (process-death stand-in).
		return injected
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "never committed"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); !errors.Is(err, injected) {
		t.Fatalf("restore error = %v, want injected", err)
	}
	// In-process path already rolled back files on DB failure.
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldSource)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("db prefix = %q after failed restore", got)
	}

	// Re-seed crash-between-phases journal and recover as on next boot.
	newBody := testModuleSource("Owned", "never committed")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldSource)
	if err := journal.markFilesApplied(state); err != nil {
		t.Fatal(err)
	}
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldSource)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared: %v", err)
	}
}

func TestFinishForwardCommitLeavesDBAppliedIfRemoveFails(t *testing.T) {
	// If remove is interrupted after db_applied, recovery must forward-commit (keep FS).
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := testModuleSource("Owned", "forward")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), body, 0600); err != nil {
		t.Fatal(err)
	}
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name:    "owned.go",
		Install: true,
		Existed: false,
		Applied: true,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": body}, ""); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Applied = true
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	if err := journal.markDBApplied(state); err != nil {
		t.Fatal(err)
	}
	// Crash after db_applied, before remove: recovery drops journal, keeps FS.
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), body)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("db_applied journal not cleared: %v", err)
	}
}

func TestRestoreJournalDBApplyingWithMatchingRestoreIDForwardCommits(t *testing.T) {
	// Crash window residual closed when DB already carries restore_id:
	// journal at db_applying + live FS rolled/wrong → re-apply from staged, keep DB.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "db committed new")
	// Live FS still looks old (as if recovery would otherwise roll back).
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	// seed wrote newBody to live; put old body back to simulate divergence after DB commit.
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
	}, state.RestoreID, state.PayloadHash)); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("db prefix = %q, want new (forward commit)", got)
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after forward recovery: %v", err)
	}
}

func TestRestoreJournalDBApplyingWithoutRestoreIDRollsBackFS(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "db not yet committed")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("db prefix = %q, want old", got)
	}
}

func TestRestoreJournalDBApplyingMismatchedRestoreIDRollsBackFS(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "other restore id")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	// DB carries a different restore_id (previous successful restore).
	if err := db.Reset(stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "old"},
	}, "not-this-restore", "deadbeef")); err != nil {
		t.Fatal(err)
	}

	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
}

func TestApplyRestoreStampsRestoreIDIntoDatabase(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), []byte("package modules\n// old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	var stampedID, stampedHash string
	var phaseAtDB string
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		journal := openRestoreJournal()
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		phaseAtDB = state.Phase
		if owner := data[restoreDBOwner]; owner != nil {
			if v, ok := owner[restoreDBIDKey].(string); ok {
				stampedID = v
			}
			if v, ok := owner[restoreDBHashKey].(string); ok {
				stampedHash = v
			}
		}
		if stampedID == "" || stampedID != state.RestoreID {
			t.Fatalf("reset payload restore_id = %q, journal = %q", stampedID, state.RestoreID)
		}
		if stampedHash == "" || stampedHash != state.PayloadHash {
			t.Fatalf("reset payload hash = %q, journal = %q", stampedHash, state.PayloadHash)
		}
		return db.Reset(data)
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "stamped"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatal(err)
	}
	if phaseAtDB != restorePhaseDBApplying {
		t.Fatalf("phase at Reset = %q, want %q", phaseAtDB, restorePhaseDBApplying)
	}
	if got := db.GetString(restoreDBOwner, restoreDBIDKey, ""); got == "" {
		t.Fatal("restore_id not persisted in database after success")
	}
	if got := db.GetString(restoreDBOwner, restoreDBHashKey, ""); got == "" {
		t.Fatal("fs_payload_hash not persisted in database after success")
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), testModuleSource("Owned", "stamped"))
}

func TestApplyRestoreKeepsStagedForCrashRecovery(t *testing.T) {
	// staged/ must remain until finish so db_applying+restore_id recovery can re-apply.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), []byte("package modules\n// old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"owned": "local"}},
	}); err != nil {
		t.Fatal(err)
	}

	newBody := testModuleSource("Owned", "staged retained")
	var stagedAtDB []byte
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		journal := openRestoreJournal()
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		stagedAtDB, err = os.ReadFile(filepath.Join(journal.stagedDir(), "owned.go"))
		if err != nil {
			t.Fatalf("staged missing at Database.Reset: %v", err)
		}
		if state.RestoreID == "" || state.PayloadHash == "" {
			t.Fatalf("journal missing dual-commit fields: %+v", state)
		}
		return db.Reset(data)
	}
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     newBody,
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stagedAtDB, newBody) {
		t.Fatalf("staged at Reset = %q, want %q", stagedAtDB, newBody)
	}
}

func TestRestoreJournalPreparedPhaseDiscardsWithoutTouchingFS(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("package modules\n// untouched\n")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), body, 0600); err != nil {
		t.Fatal(err)
	}
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{Name: "owned.go", Install: true, Existed: true, Mode: 0600}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": testModuleSource("Owned", "never applied")}, ""); err != nil {
		t.Fatal(err)
	}
	// Leave at prepared (begin default).
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), body)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("prepared journal not cleared: %v", err)
	}
}

func TestRestoreJournalUnknownPhaseWithMatchingRestoreIDForwardCommits(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old\n")
	newBody := testModuleSource("Owned", "unknown forward")
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.Phase = "corrupted-phase-token"
	if err := journal.writeState(state); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
	}, state.RestoreID, state.PayloadHash)); err != nil {
		t.Fatal(err)
	}
	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
}

func TestRestoreJournalCrashAfterDBCommitBeforeDBAppliedForwardOnBoot(t *testing.T) {
	// End-to-end: applyRestore injects restore_id; simulated death leaves journal at
	// db_applying with DB already new; next boot recovery keeps FS+DB new.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// pre\n")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	newBody := testModuleSource("Owned", "post db commit")
	var freezeJournal bool
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		if err := db.Reset(data); err != nil {
			return err
		}
		// Simulate process death after durable DB rename, before db_applied.
		// Leave journal at db_applying (already set before Reset).
		freezeJournal = true
		return errors.New("injected process death after db commit")
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     newBody,
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	err := m.restoreAllFromZip(backup)
	if err == nil {
		t.Fatal("expected injected process death error")
	}
	if !freezeJournal {
		t.Fatal("reset spy did not run")
	}
	// In-process path rolls FS because injected error is not CommitUncertain.
	// Re-seed the true crash window: FS new, journal db_applying, DB has restore_id.
	// Capture restore_id from live DB after the failed path's first Reset... but
	// applyRestore rolled DB back. Simulate pure crash: re-apply DB stamp + FS new.
	journal := openRestoreJournal()
	entries := []restoreJournalEntry{{
		Name: "owned.go", Install: true, Existed: true, Applied: true, Mode: 0600,
	}}
	if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": newBody}, db.LocalPath()); err != nil {
		t.Fatal(err)
	}
	if err := writeFileDurable(filepath.Join(journal.previousDir(), "owned.go"), oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := journal.readState()
	if err != nil {
		t.Fatal(err)
	}
	state.Entries[0].Applied = true
	if err := writeFileDurable(filepath.Join(modsDir, "owned.go"), newBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(stampRestoreCommitMetadata(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "new"},
	}, state.RestoreID, state.PayloadHash)); err != nil {
		t.Fatal(err)
	}

	// Boot recovery with live DB: must forward-commit (keep new FS + new DB).
	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("prefix = %q, want new", got)
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal left after forward boot recovery: %v", err)
	}
}

func TestRestoreJournalDBApplyingNilDBUsesPrimaryFile(t *testing.T) {
	// Residual: recovery without live Database handle probes primary on disk.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "nil db primary probe")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.DBFile = db.LocalPath()
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
	}, state.RestoreID, state.PayloadHash)); err != nil {
		t.Fatal(err)
	}
	// Close is not required: offline probe reads the primary file path.
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared: %v", err)
	}
}

func TestRestoreJournalDBApplyingNilDBUsesLastValidMarker(t *testing.T) {
	// When primary is unreadable, restore_id marker beside last-valid proves forward.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "marker forward")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	dbFile := db.LocalPath()
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.DBFile = dbFile
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	// Corrupt primary so offline file parse fails; marker still proves commit.
	if err := os.WriteFile(dbFile, []byte("not-json{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := goroku.WriteRestoreCommitMarker(dbFile, state.RestoreID); err != nil {
		t.Fatal(err)
	}
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
}

func TestRestoreJournalDBApplyingUnreadablePrimaryCommitsFromStaged(t *testing.T) {
	// Residual close: primary unreadable, no markers, retained staged present →
	// forward-commit DB from staged and keep FS new (not FS-old+DB-new).
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "staged forward commit")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	dbFile := db.LocalPath()
	if err := db.Reset(map[string]map[string]any{
		"goroku.main": {"command_prefix": "old"},
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
	}); err != nil {
		t.Fatal(err)
	}
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	// Live FS already new (post files_applied crash window).
	if err := writeFileDurable(filepath.Join(modsDir, "owned.go"), newBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.DBFile = dbFile
	stagedPayload := stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
	}, state.RestoreID, state.PayloadHash)
	stagedBody, err := json.MarshalIndent(stagedPayload, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(journal.dir, restoreJournalStagedDB)
	if err := writeFileDurable(retained, stagedBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.StagedDBPath = retained
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	// Unreadable primary + no markers + no last-valid proof.
	if err := os.WriteFile(dbFile, []byte("not-json{"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(goroku.LastValidPath(dbFile))
	_ = os.Remove(dbFile + ".restore-id")
	_ = os.Remove(goroku.LastValidPath(dbFile) + ".restore-id")

	wantRestoreID := state.RestoreID
	// Offline recovery (nil live DB): must commit from staged and keep FS new.
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
	data := mustReadDBFile(t, dbFile)
	if got := restoreIDFromDocument(data); got != wantRestoreID {
		t.Fatalf("primary restore_id = %q, want %q", got, wantRestoreID)
	}
	if got := goroku.DocumentString(data, "goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("command_prefix = %q, want new", got)
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared after staged forward: %v", err)
	}
}

func TestRestoreJournalDBApplyingUnreadablePrimaryLiveDBCommitsFromStaged(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "live staged forward")
	db := newBackupTestDB(t)
	dbFile := db.LocalPath()
	if err := db.Reset(map[string]map[string]any{
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := writeFileDurable(filepath.Join(modsDir, "owned.go"), newBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.DBFile = dbFile
	stagedPayload := stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
	}, state.RestoreID, state.PayloadHash)
	stagedBody, err := json.MarshalIndent(stagedPayload, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(journal.dir, restoreJournalStagedDB)
	if err := writeFileDurable(retained, stagedBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.StagedDBPath = retained
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	// Corrupt on-disk primary; live handle still has old memory / no restore_id.
	if err := os.WriteFile(dbFile, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}

	wantRestoreID := state.RestoreID
	if err := recoverIncompleteModuleRestore(db); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("live db prefix = %q, want new", got)
	}
	if got := db.GetString(restoreDBOwner, restoreDBIDKey, ""); got != wantRestoreID {
		t.Fatalf("live restore_id = %q, want %q", got, wantRestoreID)
	}
}

func mustReadDBFile(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	data, err := goroku.ReadLocalDatabaseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestApplyRestoreRetainsStagedDBUntilJournalCleared(t *testing.T) {
	// Journal-retained staged-db must still exist at CommitStagedReset time.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), []byte("package modules\n// old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	var retainedAtCommit string
	var retainedExisted bool
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		journal := openRestoreJournal()
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		retainedAtCommit = state.StagedDBPath
		if state.StagedDBPath == "" {
			t.Fatal("expected journal-retained staged db at commit")
		}
		if _, err := os.Stat(state.StagedDBPath); err != nil {
			t.Fatalf("retained staged missing at commit: %v", err)
		}
		retainedExisted = true
		if restoreIDFromFile(state.StagedDBPath) != state.RestoreID {
			t.Fatalf("retained staged restore_id mismatch")
		}
		return db.Reset(data)
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "retain staged"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatal(err)
	}
	if !retainedExisted || retainedAtCommit == "" {
		t.Fatal("retained staged checks did not run")
	}
	if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
		t.Fatalf("journal left after success: %v", err)
	}
	if got := goroku.ReadRestoreCommitMarker(db.LocalPath()); got == "" {
		t.Fatal("expected restore_id markers after success")
	}
	if _, err := os.Stat(db.LocalPath() + ".restore-id"); err != nil {
		t.Fatalf("primary-adjacent marker missing: %v", err)
	}
}

func TestRestoreJournalDBApplyingNilDBUsesLastValidDocument(t *testing.T) {
	// last-valid document itself may carry restore_id after a later generation.
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("package modules\n// old owned\n")
	newBody := testModuleSource("Owned", "last-valid doc")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	dbFile := db.LocalPath()
	journal, state := seedOwnedRestoreJournal(t, modsDir, newBody, oldBody)
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldBody, 0600); err != nil {
		t.Fatal(err)
	}
	state.DBFile = dbFile
	if err := journal.markDBApplying(state); err != nil {
		t.Fatal(err)
	}
	// Ensure a primary exists then remove it; last-valid holds matching restore_id.
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dbFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	lastValidBody, err := json.MarshalIndent(stampRestoreCommitMetadata(map[string]map[string]any{
		"goroku.main": {"command_prefix": "new"},
	}, state.RestoreID, state.PayloadHash), "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goroku.LastValidPath(dbFile), lastValidBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := recoverIncompleteModuleRestore(nil); err != nil {
		t.Fatal(err)
	}
	assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
}

func TestApplyRestoreStagesDBBeforeFSAndRenamesAfter(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// old\n")
	if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader":      {"loaded_modules": map[string]any{"owned": "local"}},
		"goroku.main": {"command_prefix": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	primaryBefore, err := os.ReadFile(db.LocalPath())
	if err != nil {
		t.Fatal(err)
	}

	var sawStagedBeforeFS, sawPrimaryUnchangedAtFS bool
	var phaseAtCommit string
	m := newBackupTestModule(db)
	m.restoreApplyFile = func(source, destination string) error {
		// During FS apply, journal should already record a staged DB candidate
		// and primary must still be pre-restore.
		journal := openRestoreJournal()
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		if state.StagedDBPath == "" {
			t.Fatal("expected staged DB path before FS apply")
		}
		if _, err := os.Stat(state.StagedDBPath); err != nil {
			t.Fatalf("staged DB missing during FS apply: %v", err)
		}
		sawStagedBeforeFS = true
		primaryNow, err := os.ReadFile(db.LocalPath())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(primaryNow, primaryBefore) {
			t.Fatal("primary changed before FS apply completed")
		}
		sawPrimaryUnchangedAtFS = true
		body, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return writeFileDurable(destination, body, 0600)
	}
	m.restoreDBReset = func(data map[string]map[string]any) error {
		journal := openRestoreJournal()
		state, err := journal.readState()
		if err != nil {
			t.Fatal(err)
		}
		phaseAtCommit = state.Phase
		if state.Phase != restorePhaseDBApplying {
			t.Fatalf("phase at DB commit = %q, want %q", state.Phase, restorePhaseDBApplying)
		}
		// FS must already match backup when rename runs.
		assertFileBody(t, filepath.Join(modsDir, "owned.go"), testModuleSource("Owned", "stage order"))
		return db.Reset(data)
	}

	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "stage order"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"Loader":{"loaded_modules":{"owned":"local"}},"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})
	if err := m.restoreAllFromZip(backup); err != nil {
		t.Fatal(err)
	}
	if !sawStagedBeforeFS || !sawPrimaryUnchangedAtFS {
		t.Fatalf("staging order checks failed: staged=%v primaryStable=%v", sawStagedBeforeFS, sawPrimaryUnchangedAtFS)
	}
	if phaseAtCommit != restorePhaseDBApplying {
		t.Fatalf("phase at commit = %q", phaseAtCommit)
	}
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("prefix = %q, want new", got)
	}
	if got := goroku.ReadRestoreCommitMarker(db.LocalPath()); got == "" {
		t.Fatal("expected restore_id marker beside last-valid after success")
	}
	if got := db.GetString(restoreDBOwner, restoreDBIDKey, ""); got == "" {
		t.Fatal("restore_id missing from DB")
	}
}

func TestRestoreJournalCrashWindowsMatrix(t *testing.T) {
	// Exhaustive phase × DB-presence matrix for dual-commit recovery.
	type outcome int
	const (
		wantFSOld outcome = iota
		wantFSNew
		wantDiscardOnly
	)
	cases := []struct {
		name    string
		phase   string
		withDB  bool   // live handle with matching restore_id
		offline string // "", "primary", "marker", "last-valid"
		want    outcome
	}{
		{name: "prepared", phase: restorePhasePrepared, want: wantDiscardOnly},
		{name: "applying", phase: restorePhaseApplying, want: wantFSOld},
		{name: "files_applied", phase: restorePhaseFilesApplied, want: wantFSOld},
		{name: "db_applying_no_proof", phase: restorePhaseDBApplying, want: wantFSOld},
		{name: "db_applying_live_db", phase: restorePhaseDBApplying, withDB: true, want: wantFSNew},
		{name: "db_applying_primary", phase: restorePhaseDBApplying, offline: "primary", want: wantFSNew},
		{name: "db_applying_marker", phase: restorePhaseDBApplying, offline: "marker", want: wantFSNew},
		{name: "db_applying_last_valid", phase: restorePhaseDBApplying, offline: "last-valid", want: wantFSNew},
		{name: "db_applying_staged", phase: restorePhaseDBApplying, offline: "staged", want: wantFSNew},
		{name: "db_applied", phase: restorePhaseDBApplied, want: wantFSNew},
		{name: "empty_no_proof", phase: "", want: wantFSOld},
		{name: "empty_with_db", phase: "", withDB: true, want: wantFSNew},
		{name: "unknown_no_proof", phase: "weird", want: wantFSOld},
		{name: "unknown_with_marker", phase: "weird", offline: "marker", want: wantFSNew},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataRoot, _ := setModuleTestRoots(t)
			modsDir := filepath.Join(dataRoot, "modules")
			if err := os.MkdirAll(modsDir, 0700); err != nil {
				t.Fatal(err)
			}
			oldBody := []byte("package modules\n// old\n")
			newBody := testModuleSource("Owned", "matrix-"+tc.name)
			// Live FS starts as NEW (post-apply crash window) except prepared.
			live := newBody
			if tc.phase == restorePhasePrepared {
				live = oldBody
			}
			if err := os.WriteFile(filepath.Join(modsDir, "owned.go"), live, 0600); err != nil {
				t.Fatal(err)
			}
			db := newBackupTestDB(t)
			journal := openRestoreJournal()
			entries := []restoreJournalEntry{{
				Name: "owned.go", Install: true, Existed: true, Applied: true, Mode: 0600,
			}}
			if err := journal.begin(modsDir, false, entries, map[string][]byte{"owned.go": newBody}, db.LocalPath()); err != nil {
				t.Fatal(err)
			}
			if err := writeFileDurable(filepath.Join(journal.previousDir(), "owned.go"), oldBody, 0600); err != nil {
				t.Fatal(err)
			}
			state, err := journal.readState()
			if err != nil {
				t.Fatal(err)
			}
			state.Entries[0].Applied = true
			state.Phase = tc.phase
			if err := journal.writeState(state); err != nil {
				t.Fatal(err)
			}

			var recoverDB *goroku.Database
			stamped := stampRestoreCommitMetadata(map[string]map[string]any{
				"goroku.main": {"command_prefix": "new"},
			}, state.RestoreID, state.PayloadHash)
			switch {
			case tc.withDB:
				if err := db.Reset(stamped); err != nil {
					t.Fatal(err)
				}
				recoverDB = db
			case tc.offline == "primary":
				if err := db.Reset(stamped); err != nil {
					t.Fatal(err)
				}
			case tc.offline == "marker":
				if err := os.WriteFile(db.LocalPath(), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := goroku.WriteRestoreCommitMarker(db.LocalPath(), state.RestoreID); err != nil {
					t.Fatal(err)
				}
			case tc.offline == "last-valid":
				if err := os.Remove(db.LocalPath()); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				body, err := json.MarshalIndent(stamped, "", "    ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goroku.LastValidPath(db.LocalPath()), body, 0600); err != nil {
					t.Fatal(err)
				}
			case tc.offline == "staged":
				// Unreadable primary, no markers; retained staged carries restore_id.
				if err := os.WriteFile(db.LocalPath(), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
				body, err := json.MarshalIndent(stamped, "", "    ")
				if err != nil {
					t.Fatal(err)
				}
				retained := filepath.Join(journal.dir, restoreJournalStagedDB)
				if err := writeFileDurable(retained, body, 0600); err != nil {
					t.Fatal(err)
				}
				state.StagedDBPath = retained
				if err := journal.writeState(state); err != nil {
					t.Fatal(err)
				}
			}

			if err := recoverIncompleteModuleRestore(recoverDB); err != nil {
				t.Fatal(err)
			}
			switch tc.want {
			case wantFSOld:
				assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
			case wantFSNew:
				assertFileBody(t, filepath.Join(modsDir, "owned.go"), newBody)
			case wantDiscardOnly:
				assertFileBody(t, filepath.Join(modsDir, "owned.go"), oldBody)
			}
			if _, err := os.Stat(restoreJournalRoot()); !os.IsNotExist(err) {
				t.Fatalf("journal not cleared for %s: %v", tc.name, err)
			}
		})
	}
}

func TestRestoreReducedManifestRemovalRollsBackOnDatabaseFailure(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldRemoved := testModuleSource("Removed", "old removed")
	removedPath := filepath.Join(modsDir, "removed.go")
	if err := os.WriteFile(removedPath, oldRemoved, 0640); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	oldDB := map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"removed": "local"}},
	}
	if err := db.Reset(oldDB); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected forward reset failure")
	resetCalls := 0
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		resetCalls++
		if resetCalls == 1 {
			return injected
		}
		return db.Reset(data)
	}

	err := m.restoreModulesFromData(makeZip(t, map[string][]byte{"db_mods.json": []byte(`{}`)}))
	if !errors.Is(err, injected) {
		t.Fatalf("restore error = %v, want forward failure", err)
	}
	assertFileBody(t, removedPath, oldRemoved)
	if info, statErr := os.Stat(removedPath); statErr != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("restored source mode = %v, %v", info, statErr)
	}
	loaded, loadErr := m.loadedModulesMapChecked()
	if loadErr != nil || loaded["removed"] != "local" {
		t.Fatalf("rolled-back manifest = %#v, %v", loaded, loadErr)
	}
}

func TestRestoreLaterFileApplyFailureRollsBackFilesAndDB(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldA := []byte("package modules\n// old a\n")
	oldB := []byte("package modules\n// old b\n")
	if err := os.WriteFile(filepath.Join(modsDir, "a.go"), oldA, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "b.go"), oldB, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	applyCalls := 0
	m := &GorokuBackup{
		Base:                    goroku.Base{DB: db},
		compileModuleValidation: acceptModuleCompilation,
		restoreApplyFile: func(source, destination string) error {
			applyCalls++
			if applyCalls == 2 {
				return fmt.Errorf("injected second-file failure")
			}
			return os.Rename(source, destination)
		},
	}
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"a":"local","b":"local"}`),
		"a.go":         testModuleSource("A", "new a"),
		"b.go":         testModuleSource("B", "new b"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})

	if err := m.restoreAllFromZip(backup); err == nil {
		t.Fatal("injected apply failure was ignored")
	}
	assertFileBody(t, filepath.Join(modsDir, "a.go"), oldA)
	assertFileBody(t, filepath.Join(modsDir, "b.go"), oldB)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("database changed before file apply completed: %q", got)
	}
}

func TestRestoreDBFailureRollsBackAppliedFilesAndDB(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// old\n")
	modulePath := filepath.Join(modsDir, "owned.go")
	if err := os.WriteFile(modulePath, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	resetCalls := 0
	injected := errors.New("injected database failure")
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		resetCalls++
		saved := db.Reset(data)
		if resetCalls == 1 {
			return injected
		}
		return saved
	}
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     testModuleSource("Owned", "new"),
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})

	if err := m.restoreAllFromZip(backup); !errors.Is(err, injected) {
		t.Fatalf("restore error = %v, want injected persistence failure", err)
	} else if err == nil {
		t.Fatal("injected database failure was ignored")
	}
	if resetCalls != 2 {
		t.Fatalf("Database.Reset called %d times, want restore plus rollback", resetCalls)
	}
	assertFileBody(t, modulePath, oldSource)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "old" {
		t.Fatalf("database rollback left command prefix %q", got)
	}
}

func TestRestoreCommittedWarningKeepsDatabaseAndAppliedFilesAligned(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(modsDir, "owned.go")
	oldSource := []byte("package modules\n// old\n")
	newSource := testModuleSource("Owned", "new")
	if err := os.WriteFile(modulePath, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{"goroku.main": {"command_prefix": "old"}}); err != nil {
		t.Fatal(err)
	}
	resetCalls := 0
	warning := &goroku.DatabaseError{
		Operation: "reset",
		Backend:   "file",
		Committed: true,
		Err:       errors.Join(goroku.ErrDatabasePersistence, goroku.ErrDatabaseCommitUncertain),
	}
	m := newBackupTestModule(db)
	m.restoreDBReset = func(data map[string]map[string]any) error {
		resetCalls++
		if err := db.Reset(data); err != nil {
			return err
		}
		return warning
	}
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     newSource,
	})
	backup := makeZip(t, map[string][]byte{
		"db.json":  []byte(`{"goroku.main":{"command_prefix":"new"}}`),
		"mods.zip": mods,
	})

	err := m.restoreAllFromZip(backup)
	if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		t.Fatalf("restore error = %v, want committed durability warning", err)
	}
	if resetCalls != 1 {
		t.Fatalf("Database.Reset called %d times, want no rollback", resetCalls)
	}
	assertFileBody(t, modulePath, newSource)
	if got := db.GetString("goroku.main", "command_prefix", ""); got != "new" {
		t.Fatalf("committed database was rolled back to %q", got)
	}
	loaded, readErr := m.loadedModulesMapChecked()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if loaded["owned"] != "local" {
		t.Fatalf("committed manifest = %#v", loaded)
	}
}

func TestCompleteRestoreSchedulesRestartExactlyOnceForCommittedWarning(t *testing.T) {
	warning := &forwardRestoreCommitWarning{err: &goroku.DatabaseError{
		Operation: "reset",
		Committed: true,
		Err:       errors.Join(goroku.ErrDatabasePersistence, goroku.ErrDatabaseCommitUncertain),
	}}
	restarts := 0
	notifications := 0
	m := &GorokuBackup{scheduleRestart: func() { restarts++ }}

	err := m.completeRestore(warning, func(got error) {
		notifications++
		if !errors.Is(got, goroku.ErrDatabaseCommitUncertain) {
			t.Fatalf("notification warning = %v", got)
		}
	})
	if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		t.Fatalf("completeRestore() error = %v", err)
	}
	if restarts != 1 || notifications != 1 {
		t.Fatalf("restart/notification calls = %d/%d, want 1/1", restarts, notifications)
	}

	ordinary := errors.New("uncommitted reset failure")
	if err := m.completeRestore(ordinary, func(error) { notifications++ }); !errors.Is(err, ordinary) {
		t.Fatalf("ordinary completeRestore() error = %v", err)
	}
	if restarts != 1 || notifications != 1 {
		t.Fatalf("ordinary failure scheduled restart or notification: %d/%d", restarts, notifications)
	}
}

func TestCompleteRestoreDoesNotTreatRollbackUncertaintyAsForwardCommit(t *testing.T) {
	rollbackWarning := errors.Join(
		errors.New("forward restore failed"),
		fmt.Errorf("database rollback failed: %w", goroku.ErrDatabaseCommitUncertain),
	)
	restarts := 0
	notifications := 0
	m := &GorokuBackup{scheduleRestart: func() { restarts++ }}

	err := m.completeRestore(rollbackWarning, func(error) { notifications++ })
	if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		t.Fatalf("completeRestore() lost rollback warning: %v", err)
	}
	if restarts != 0 || notifications != 0 {
		t.Fatalf("rollback warning reported successful restore: restart/notify = %d/%d", restarts, notifications)
	}
}

func TestBackupRedactsInlineAndModuleMetadataSecrets(t *testing.T) {
	db := newBackupTestDB(t)
	client := goroku.NewCustomTelegramClient(1)
	client.Loader = goroku.NewModules(client, db)
	if err := client.Loader.RegisterModule(&backupSecretTestModule{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(map[string]map[string]any{
		"goroku.inline":    {"bot_token": "123456:INLINE_SECRET"},
		"BackupSecretTest": {"credential": "MODULE_SECRET", "public": "visible"},
	}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	m.Client = client

	backup, err := m.buildDBJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(backup, []byte("INLINE_SECRET")) || bytes.Contains(backup, []byte("MODULE_SECRET")) {
		t.Fatalf("backup disclosed a known secret: %s", backup)
	}
	if count := bytes.Count(backup, []byte(backupSecretMarkerKey)); count != 2 {
		t.Fatalf("backup secret marker count = %d, want 2: %s", count, backup)
	}
	if !bytes.Contains(backup, []byte(`"public": "visible"`)) {
		t.Fatalf("backup removed non-secret config: %s", backup)
	}
}

func TestRestorePreservesExistingSecretsInsteadOfWritingMarker(t *testing.T) {
	db := newBackupTestDB(t)
	client := goroku.NewCustomTelegramClient(1)
	client.Loader = goroku.NewModules(client, db)
	if err := client.Loader.RegisterModule(&backupSecretTestModule{}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	m.Client = client
	if err := db.Reset(map[string]map[string]any{
		"goroku.inline":    {"bot_token": "old-inline"},
		"BackupSecretTest": {"credential": "old-module", "public": "from-backup"},
	}); err != nil {
		t.Fatal(err)
	}
	backup, err := m.buildDBJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(map[string]map[string]any{
		"goroku.inline":    {"bot_token": "live-inline"},
		"BackupSecretTest": {"credential": "live-module", "public": "current"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.restoreDatabaseFromData(backup); err != nil {
		t.Fatal(err)
	}
	if got := db.GetString("goroku.inline", "bot_token", ""); got != "live-inline" {
		t.Fatalf("inline token after restore = %q", got)
	}
	if got := db.GetString("BackupSecretTest", "credential", ""); got != "live-module" {
		t.Fatalf("module credential after restore = %q", got)
	}
	if got := db.GetString("BackupSecretTest", "public", ""); got != "from-backup" {
		t.Fatalf("non-secret config after restore = %q", got)
	}
}

func TestBackupLifecyclePeriodReadsPropagateUnavailableDatabase(t *testing.T) {
	db := goroku.NewDatabase(1)
	if err := (&GorokuBackup{}).Init(nil, db); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("Init() error = %v, want database lifecycle error", err)
	}

	m := &GorokuBackup{Base: goroku.Base{DB: db}, backupPeriod: 6 * time.Hour}
	if err := m.ClientReady(); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("ClientReady() error = %v, want database lifecycle error", err)
	}
	if err := m.reloadBackupPeriod(); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("reloadBackupPeriod() error = %v, want database lifecycle error", err)
	}
	if m.backupPeriod != 6*time.Hour {
		t.Fatalf("failed scheduler read disabled period: %v", m.backupPeriod)
	}
}

func TestUnavailableManifestReadCannotEmitIncompleteBackup(t *testing.T) {
	setModuleTestRoots(t)
	m := newBackupTestModule(goroku.NewDatabase(1))
	if _, err := m.loadedModulesMapChecked(); !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("loadedModulesMapChecked() error = %v, want database lifecycle error", err)
	}
	archive, err := m.buildArchive()
	if !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("buildArchive() error = %v, want database lifecycle error", err)
	}
	if archive != nil {
		t.Fatalf("buildArchive() emitted %d bytes after manifest read failure", len(archive))
	}
}

func TestSetBackupPeriodPersistenceFailureDoesNotMarkScheduleSuccessful(t *testing.T) {
	oldPeriod := 12 * time.Hour
	oldBackup := time.Unix(123, 0)
	m := &GorokuBackup{
		Base:         goroku.Base{DB: goroku.NewDatabase(1)},
		backupPeriod: oldPeriod,
		lastBackup:   oldBackup,
	}

	err := m.setBackupPeriod(6, time.Unix(456, 0))
	if !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("setBackupPeriod() error = %v", err)
	}
	if m.backupPeriod != oldPeriod || !m.lastBackup.Equal(oldBackup) {
		t.Fatalf("failed persistence changed schedule to %v at %v", m.backupPeriod, m.lastBackup)
	}
}

func TestUpdaterPrepareRestartPersistenceFailureDoesNotRestart(t *testing.T) {
	restarted := false
	m := &Updater{
		Base:    goroku.Base{DB: goroku.NewDatabase(1)},
		restart: func() { restarted = true },
	}

	err := m.prepareRestart("1:2")
	if !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("prepareRestart() error = %v", err)
	}
	if restarted {
		t.Fatal("restart was scheduled after persistence failure")
	}
}

func TestRestoreRollbackErrorPreservesDatabaseError(t *testing.T) {
	m := newBackupTestModule(goroku.NewDatabase(1))
	err := m.applyRestore(map[string]map[string]any{"owner": {"key": "value"}}, nil)
	if !errors.Is(err, goroku.ErrDatabaseNotInitialized) {
		t.Fatalf("applyRestore() error = %v, want database cause", err)
	}
	var dbErr *goroku.DatabaseError
	if !errors.As(err, &dbErr) {
		t.Fatalf("applyRestore() error = %v, want DatabaseError", err)
	}
}

func TestRestoreModulesMalformedSourceLeavesStateUnchanged(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	modsDir := filepath.Join(dataRoot, "modules")
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldSource := []byte("package modules\n// old\n")
	modulePath := filepath.Join(modsDir, "owned.go")
	if err := os.WriteFile(modulePath, oldSource, 0600); err != nil {
		t.Fatal(err)
	}
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"old": "local"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := newBackupTestModule(db)
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"owned":"local"}`),
		"owned.go":     []byte("not go source"),
	})

	if err := m.restoreModulesFromData(mods); err == nil {
		t.Fatal("malformed module source was accepted")
	}
	assertFileBody(t, modulePath, oldSource)
	got, err := m.loadedModulesMapChecked()
	if err != nil {
		t.Fatal(err)
	}
	if got["old"] != "local" || len(got) != 1 {
		t.Fatalf("module manifest changed: %#v", got)
	}
}

func TestRestoreDatabaseRejectsInvalidTopLevelPayloadWithoutReset(t *testing.T) {
	for _, payload := range []string{"null", `{}`, `[]`, `{"owner":null}`} {
		t.Run(payload, func(t *testing.T) {
			db := newBackupTestDB(t)
			if err := db.Reset(map[string]map[string]any{"owner": {"value": "old"}}); err != nil {
				t.Fatal(err)
			}
			resetCalls := 0
			m := &GorokuBackup{Base: goroku.Base{DB: db}, compileModuleValidation: acceptModuleCompilation, restoreDBReset: func(data map[string]map[string]any) error {
				resetCalls++
				return db.Reset(data)
			}}

			if err := m.restoreDatabaseFromData([]byte(payload)); err == nil {
				t.Fatalf("restoreDatabaseFromData(%s) succeeded", payload)
			}
			if resetCalls != 0 {
				t.Fatalf("Database.Reset called %d times", resetCalls)
			}
			if got := db.GetString("owner", "value", ""); got != "old" {
				t.Fatalf("database state changed to %q", got)
			}
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("subsequent database write panicked: %v", recovered)
					}
				}()
				if err := db.Set("owner", "after_restore", true); err != nil {
					t.Fatal("subsequent database write failed")
				}
			}()
		})
	}
}

func TestRestoreModulesRequiresOneSourcePerManifestModule(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []zipTestEntry
		want  string
	}{
		{
			name:  "missing source",
			files: []zipTestEntry{{name: "db_mods.json", body: []byte(`{"owned":"local"}`)}},
			want:  "is missing",
		},
		{
			name: "undeclared source",
			files: []zipTestEntry{
				{name: "db_mods.json", body: []byte(`{}`)},
				{name: "extra.go", body: testModuleSource("Extra", "undeclared")},
			},
			want: "not owned",
		},
		{
			name: "non-normalized path",
			files: []zipTestEntry{
				{name: "db_mods.json", body: []byte(`{"owned":"local"}`)},
				{name: "dir/owned.go", body: testModuleSource("Owned", "path")},
			},
			want: "invalid module path",
		},
		{
			name: "duplicate source",
			files: []zipTestEntry{
				{name: "db_mods.json", body: []byte(`{"owned":"local"}`)},
				{name: "owned.go", body: testModuleSource("Owned", "first")},
				{name: "owned.go", body: testModuleSource("Owned", "second")},
			},
			want: "duplicate module entry",
		},
		{
			name: "non-regular source",
			files: []zipTestEntry{
				{name: "db_mods.json", body: []byte(`{"owned":"local"}`)},
				{name: "owned.go", body: testModuleSource("Owned", "mode"), mode: os.ModeSymlink | 0777},
			},
			want: "unsupported module entry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setModuleTestRoots(t)
			m := newBackupTestModule(newBackupTestDB(t))
			err := m.restoreModulesFromData(makeZipEntries(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restoreModulesFromData() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRestoreModulesRejectsLegacyManifestWithoutSources(t *testing.T) {
	m := newBackupTestModule(newBackupTestDB(t))
	if err := m.restoreModulesFromData([]byte(`{"remote":"https://example.test/remote.go"}`)); err == nil {
		t.Fatal("source-less module manifest was accepted")
	}
}

func TestRestoreModuleSourceStructure(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "no module", body: []byte("package modules\ntype Helper struct{}\n")},
		{name: "invalid method shape", body: bytes.Replace(testModuleSource("Owned", "shape"), []byte("Name() string"), []byte("Name(string) string"), 1)},
		{name: "invalid return type", body: bytes.Replace(testModuleSource("Owned", "return"), []byte("Name() string { return \"Owned\" }"), []byte("Name() int { return 1 }"), 1)},
		{name: "type error", body: bytes.Replace(testModuleSource("Owned", "type error"), []byte("return \"Owned\""), []byte("return doesNotExist"), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setModuleTestRoots(t)
			mods := makeZip(t, map[string][]byte{
				"db_mods.json": []byte(`{"owned":"local"}`),
				"owned.go":     tc.body,
			})
			m := newBackupTestModule(newBackupTestDB(t))
			m.compileModuleValidation = func(string, []byte) error {
				return fmt.Errorf("plugin build failed: deterministic test rejection")
			}
			err := m.restoreModulesFromData(mods)
			if err == nil || !strings.Contains(err.Error(), "plugin build failed") {
				t.Fatalf("restoreModulesFromData() error = %v, want compile failure", err)
			}
		})
	}
}

func TestProductionCompileValidationRejectsTypeErrorsWithoutRunningInit(t *testing.T) {
	dataRoot, _ := setModuleTestRoots(t)
	initMarker := filepath.Join(t.TempDir(), "init-ran")
	source := []byte(fmt.Sprintf(`package compatible

import (
	core "goroku/goroku"
	"os"
)

type Client = core.CustomTelegramClient
type DB = core.Database
type Command = core.CommandHandler
type Watcher = core.WatcherHandler

type AliasCompatible struct{}

func init() { _ = os.WriteFile(%q, []byte("ran"), 0600) }
func (*AliasCompatible) Name() string { return "AliasCompatible" }
func (*AliasCompatible) Strings() map[string]string { return nil }
func (*AliasCompatible) Init(*Client, *DB) error { return nil }
func (*AliasCompatible) ClientReady() error { return nil }
func (*AliasCompatible) OnUnload() error { return nil }
func (*AliasCompatible) OnDlmod() error { return nil }
func (*AliasCompatible) Commands() map[string]Command { return nil }
func (*AliasCompatible) Watchers() []Watcher { return nil }
`, initMarker))
	mods := makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"alias":"local"}`),
		"alias.go":     source,
	})

	if err := (&GorokuBackup{Base: goroku.Base{DB: newBackupTestDB(t)}}).restoreModulesFromData(mods); err != nil {
		t.Fatalf("supported module restore failed: %v", err)
	}
	if _, err := os.Stat(initMarker); !os.IsNotExist(err) {
		t.Fatalf("archive validation executed plugin init: %v", err)
	}
	assertFileBody(t, filepath.Join(dataRoot, "modules", "alias.go"), source)

	invalid := bytes.Replace(source, []byte(`return "AliasCompatible"`), []byte("return doesNotExist"), 1)
	mods = makeZip(t, map[string][]byte{
		"db_mods.json": []byte(`{"alias":"local"}`),
		"alias.go":     invalid,
	})
	if err := (&GorokuBackup{Base: goroku.Base{DB: newBackupTestDB(t)}}).restoreModulesFromData(mods); err == nil || !strings.Contains(err.Error(), "plugin build failed") {
		t.Fatalf("production validation error = %v, want type-check failure", err)
	}
	if _, err := os.Stat(initMarker); !os.IsNotExist(err) {
		t.Fatalf("failed archive validation executed plugin init: %v", err)
	}
}

func TestRestoreModuleManifestRejectsNullAndInvalidProvenance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		want     string
	}{
		{name: "null entry", manifest: `{"owned":null}`, want: "is null"},
		{name: "empty entry", manifest: `{"owned":""}`, want: "empty or invalid"},
		{name: "invalid entry", manifest: `{"owned":"ftp://example.test/owned.go"}`, want: "is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zr := openZip(t, makeZip(t, map[string][]byte{"db_mods.json": []byte(tc.manifest)}))
			_, _, err := newBackupTestModule(nil).validateModuleRestore(zr.File)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateModuleRestore() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildArchiveFailsForMissingOrInvalidDeclaredSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		writeBody []byte
		want      string
	}{
		{name: "missing", want: "unavailable"},
		{name: "invalid", writeBody: bytes.Replace(testModuleSource("Owned", "invalid backup"), []byte("return \"Owned\""), []byte("return missingName"), 1), want: "plugin build failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setModuleTestRoots(t)
			db := newBackupTestDB(t)
			if err := db.Reset(map[string]map[string]any{"Loader": {"loaded_modules": map[string]any{"owned": "local"}}}); err != nil {
				t.Fatal(err)
			}
			if tc.writeBody != nil {
				if err := ensureRuntimeModuleSourceDir(); err != nil {
					t.Fatal(err)
				}
				path, err := runtimeModuleSourcePath("owned")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, tc.writeBody, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := newBackupTestModule(db)
			if tc.writeBody != nil {
				m.compileModuleValidation = func(string, []byte) error {
					return fmt.Errorf("plugin build failed: deterministic test rejection")
				}
			}
			_, err := m.buildArchive()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("buildArchive() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildArchiveRejectsNullManifestEntry(t *testing.T) {
	setModuleTestRoots(t)
	db := newBackupTestDB(t)
	if err := db.Reset(map[string]map[string]any{
		"Loader": {"loaded_modules": map[string]any{"owned": nil}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := newBackupTestModule(db).buildArchive()
	if err == nil || !strings.Contains(err.Error(), "is null") {
		t.Fatalf("buildArchive() error = %v, want null provenance error", err)
	}
}

func TestRestoreModuleManifestLimits(t *testing.T) {
	t.Run("cardinality independent of ZIP entries", func(t *testing.T) {
		manifest := make(map[string]string, maxRestoreModules+1)
		for i := 0; i <= maxRestoreModules; i++ {
			manifest[fmt.Sprintf("module%03d", i)] = "local"
		}
		manifestBytes, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		zr := openZip(t, makeZip(t, map[string][]byte{"db_mods.json": manifestBytes}))
		_, _, err = newBackupTestModule(nil).validateModuleRestore(zr.File)
		if err == nil || !strings.Contains(err.Error(), "manifest contains more") {
			t.Fatalf("validateModuleRestore() error = %v", err)
		}
	})

	for _, tc := range []struct {
		name     string
		manifest map[string]string
		want     string
	}{
		{name: "oversized name", manifest: map[string]string{strings.Repeat("n", maxRestoreModuleNameBytes+1): "local"}, want: "module name exceeds"},
		{name: "oversized URL", manifest: map[string]string{"owned": strings.Repeat("u", maxRestoreModuleURLBytes+1)}, want: "module URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifestBytes, err := json.Marshal(tc.manifest)
			if err != nil {
				t.Fatal(err)
			}
			zr := openZip(t, makeZip(t, map[string][]byte{
				"db_mods.json": manifestBytes,
				"owned.go":     []byte("deliberately invalid source"),
			}))
			_, _, err = newBackupTestModule(nil).validateModuleRestore(zr.File)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateModuleRestore() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRestoreAllRejectsOuterDuplicatePathAndMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []zipTestEntry
		want    string
	}{
		{name: "duplicate", entries: []zipTestEntry{{name: "db.json", body: []byte(`{"owner":{}}`)}, {name: "db.json", body: []byte(`{"owner":{}}`)}}, want: "duplicate backup entry"},
		{name: "path", entries: []zipTestEntry{{name: "dir/db.json", body: []byte(`{"owner":{}}`)}}, want: "unexpected backup entry"},
		{name: "mode", entries: []zipTestEntry{{name: "db.json", body: []byte(`{"owner":{}}`), mode: os.ModeSymlink | 0777}}, want: "unsupported backup entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newBackupTestModule(newBackupTestDB(t)).restoreAllFromZip(makeZipEntries(t, tc.entries))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restoreAllFromZip() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func newBackupTestDB(t *testing.T) *goroku.Database {
	t.Helper()
	db := goroku.NewDatabase(1)
	if err := db.Init(""); err != nil {
		t.Fatal(err)
	}
	return db
}

type backupSecretTestModule struct{}

func (*backupSecretTestModule) Name() string               { return "BackupSecretTest" }
func (*backupSecretTestModule) Strings() map[string]string { return nil }
func (*backupSecretTestModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error {
	return nil
}
func (*backupSecretTestModule) ClientReady() error { return nil }
func (*backupSecretTestModule) OnUnload() error    { return nil }
func (*backupSecretTestModule) OnDlmod() error     { return nil }
func (*backupSecretTestModule) Commands() map[string]goroku.CommandHandler {
	return nil
}
func (*backupSecretTestModule) Watchers() []goroku.WatcherHandler { return nil }
func (*backupSecretTestModule) ConfigDefaults() map[string]any {
	return map[string]any{"credential": "", "public": ""}
}
func (*backupSecretTestModule) ConfigValidators() map[string]goroku.Validator {
	return map[string]goroku.Validator{
		"credential": &goroku.HiddenValidator{Inner: &goroku.StringValidator{}},
		"public":     &goroku.StringValidator{},
	}
}

func newBackupTestModule(db *goroku.Database) *GorokuBackup {
	return &GorokuBackup{Base: goroku.Base{DB: db}, compileModuleValidation: acceptModuleCompilation}
}

func acceptModuleCompilation(string, []byte) error { return nil }

func assertFileBody(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file %s = %q, %v; want %q", path, got, err, want)
	}
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type zipTestEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func makeZipEntries(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testModuleSource(typeName, marker string) []byte {
	return []byte(fmt.Sprintf(`package modules

import "goroku/goroku"

// %s
type %s struct{}

func (*%s) Name() string { return %q }
func (*%s) Strings() map[string]string { return nil }
func (*%s) Init(*goroku.CustomTelegramClient, *goroku.Database) error { return nil }
func (*%s) ClientReady() error { return nil }
func (*%s) OnUnload() error { return nil }
func (*%s) OnDlmod() error { return nil }
func (*%s) Commands() map[string]goroku.CommandHandler { return nil }
func (*%s) Watchers() []goroku.WatcherHandler { return nil }
`, marker, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName, typeName))
}

func openZip(t *testing.T, data []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func setModuleTestRoots(t *testing.T) (string, string) {
	t.Helper()
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })
	return dataRoot, sourceRoot
}

func moduleBodyFromBackup(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	outer := openZip(t, archive)
	for _, file := range outer.File {
		if file.Name != "mods.zip" {
			continue
		}
		modsBytes, err := readZipFile(file)
		if err != nil {
			t.Fatal(err)
		}
		mods := openZip(t, modsBytes)
		for _, mod := range mods.File {
			if mod.Name == name {
				r, err := mod.Open()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				body, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				return body
			}
		}
	}
	t.Fatalf("module %q not found in backup", name)
	return nil
}
