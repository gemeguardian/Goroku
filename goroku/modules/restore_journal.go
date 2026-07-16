package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"goroku/goroku"
)

// Restore journal residual crash windows (M1.1):
//
// applyRestore cannot make filesystem module sources and Database.Reset jointly
// crash-atomic without a multi-store coordinator. Database.Reset is already
// single-file atomic (temp write + rename) but has no staging-path API that can
// be renamed in lockstep with the module tree, so recovery stays journal-based.
//
// Order is intentionally FS-then-DB so an incomplete restore can be rolled back
// to the pre-restore filesystem that still matches the live (old) database when
// the DB side is still pre-restore or uncertain.
//
// Success path phase advances (each writeState is fsynced via writeFileDurable):
//
//	prepared → applying → files_applied → db_applying → db_applied → remove
//
// Phases recorded under BaseDir/.goroku-restore-journal:
//
//	prepared      – previous/ + staged/ durable; live FS and DB unchanged.
//	                Crash → discard journal (no live mutation).
//	applying      – some live module files may already match the backup
//	                (including after mutation but before Applied is journaled).
//	                Crash → rollback the full intended entry set from previous/.
//	files_applied – live FS matches backup; DB still pre-restore (Reset not started).
//	                Crash → rollback full intended FS set so boot sees FS+DB old.
//	db_applying   – FS matches backup; Database.Reset may be in flight or
//	                durability is uncertain from the journal's point of view.
//	                Crash → prefer safe FS rollback (may leave FS-old/DB-new if
//	                Reset already committed — residual below).
//	db_applied    – FS and DB both match backup; journal not yet removed.
//	                Crash → drop journal (forward commit).
//
// Residual (not jointly atomic):
//   - Power loss after Database.Reset is logically committed but before
//     db_applied is durable: recovery still prefers FS rollback when the phase
//     is files_applied/db_applying/unknown, which can yield FS-old + DB-new.
//   - Same class as ErrDatabaseCommitUncertain: the journal alone cannot prove
//     DB durability across process death.
//   - True joint atomicity needs a multi-store coordinator or a DB API that
//     stages a candidate and renames only after FS commit is durable.
//
// Ambiguous phases (empty, unknown, unreadable body that still parses with an
// unexpected phase): always prefer safe FS rollback over forward commit.

const (
	restoreJournalDirName   = ".goroku-restore-journal"
	restoreJournalStateFile = "state.json"
	restoreJournalPrev      = "previous"
	restoreJournalStaged    = "staged"

	restorePhasePrepared     = "prepared"
	restorePhaseApplying     = "applying"
	restorePhaseFilesApplied = "files_applied"
	restorePhaseDBApplying   = "db_applying"
	restorePhaseDBApplied    = "db_applied"
)

type restoreJournalEntry struct {
	Name    string `json:"name"`
	Install bool   `json:"install"`
	Existed bool   `json:"existed"`
	Applied bool   `json:"applied"`
	Mode    uint32 `json:"mode,omitempty"`
}

type restoreJournalState struct {
	Phase          string                `json:"phase"`
	ModsDir        string                `json:"mods_dir"`
	CreatedModsDir bool                  `json:"created_mods_dir"`
	Entries        []restoreJournalEntry `json:"entries"`
}

type restoreJournal struct {
	dir string
}

func restoreJournalRoot() string {
	return filepath.Join(goroku.BaseDir, restoreJournalDirName)
}

func openRestoreJournal() *restoreJournal {
	return &restoreJournal{dir: restoreJournalRoot()}
}

func (j *restoreJournal) statePath() string   { return filepath.Join(j.dir, restoreJournalStateFile) }
func (j *restoreJournal) previousDir() string { return filepath.Join(j.dir, restoreJournalPrev) }
func (j *restoreJournal) stagedDir() string   { return filepath.Join(j.dir, restoreJournalStaged) }

func (j *restoreJournal) exists() bool {
	_, err := os.Stat(j.statePath())
	return err == nil
}

func (j *restoreJournal) remove() error {
	if err := os.RemoveAll(j.dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncParentDir(j.dir)
}

func syncParentDir(path string) error {
	dir := filepath.Dir(path)
	f, err := os.Open(dir) //nolint:gosec
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTTY) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY, 0) //nolint:gosec
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTTY) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

func writeFileDurable(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".restore-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTTY) {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return syncParentDir(path)
}

func (j *restoreJournal) writeState(state *restoreJournalState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileDurable(j.statePath(), data, 0600); err != nil {
		return err
	}
	return syncParentDir(j.dir)
}

func (j *restoreJournal) readState() (*restoreJournalState, error) {
	data, err := os.ReadFile(j.statePath()) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var state restoreJournalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (j *restoreJournal) begin(modsDir string, createdModsDir bool, entries []restoreJournalEntry, stagedSources map[string][]byte) error {
	if err := j.remove(); err != nil {
		return err
	}
	if err := os.MkdirAll(j.previousDir(), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(j.stagedDir(), 0700); err != nil {
		_ = j.remove()
		return err
	}
	for name, body := range stagedSources {
		if err := writeFileDurable(filepath.Join(j.stagedDir(), name), body, 0600); err != nil {
			_ = j.remove()
			return err
		}
	}
	for i := range entries {
		if !entries[i].Existed {
			continue
		}
		src := filepath.Join(modsDir, entries[i].Name)
		body, err := os.ReadFile(src) //nolint:gosec
		if err != nil {
			_ = j.remove()
			return err
		}
		mode := os.FileMode(entries[i].Mode)
		if mode == 0 {
			mode = 0600
		}
		if err := writeFileDurable(filepath.Join(j.previousDir(), entries[i].Name), body, mode); err != nil {
			_ = j.remove()
			return err
		}
	}
	state := &restoreJournalState{
		Phase:          restorePhasePrepared,
		ModsDir:        modsDir,
		CreatedModsDir: createdModsDir,
		Entries:        entries,
	}
	if err := j.writeState(state); err != nil {
		_ = j.remove()
		return err
	}
	return nil
}

func (j *restoreJournal) markApplying(state *restoreJournalState) error {
	state.Phase = restorePhaseApplying
	return j.writeState(state)
}

func (j *restoreJournal) markEntryApplied(state *restoreJournalState, index int) error {
	if index < 0 || index >= len(state.Entries) {
		return fmt.Errorf("restore journal entry index %d out of range", index)
	}
	state.Entries[index].Applied = true
	state.Phase = restorePhaseApplying
	return j.writeState(state)
}

func (j *restoreJournal) markFilesApplied(state *restoreJournalState) error {
	state.Phase = restorePhaseFilesApplied
	return j.writeState(state)
}

func (j *restoreJournal) markDBApplying(state *restoreJournalState) error {
	state.Phase = restorePhaseDBApplying
	return j.writeState(state)
}

func (j *restoreJournal) markDBApplied(state *restoreJournalState) error {
	state.Phase = restorePhaseDBApplied
	return j.writeState(state)
}

// finishForwardCommit records db_applied (fsynced) then removes the journal.
// After FS+DB already match the backup, leaving the journal at files_applied or
// db_applying is dangerous (boot would roll FS back). Prefer durable db_applied,
// then clear; if phase advance fails, still attempt remove so no journal remains.
func (j *restoreJournal) finishForwardCommit(state *restoreJournalState) error {
	if err := j.markDBApplied(state); err != nil {
		if remErr := j.remove(); remErr != nil {
			return errors.Join(fmt.Errorf("mark db_applied: %w", err), fmt.Errorf("clear restore journal: %w", remErr))
		}
		return nil
	}
	if err := j.remove(); err != nil {
		// db_applied is durable: recovery will drop the journal on next boot.
		return nil
	}
	return nil
}

// rollbackRestoreJournalState restores the pre-restore module tree for the
// journal's intended entry set. For incomplete phases we intentionally ignore
// Applied flags: a crash between a live FS mutation and the journal update must
// still roll FS back to match the still-old database.
func rollbackRestoreJournalState(state *restoreJournalState, journal *restoreJournal) error {
	var rollbackErr error
	modsDir := state.ModsDir
	if modsDir == "" {
		modsDir = runtimeModuleSourceDir()
	}
	for i := len(state.Entries) - 1; i >= 0; i-- {
		entry := state.Entries[i]
		destination := filepath.Join(modsDir, entry.Name)
		if entry.Existed {
			prev := filepath.Join(journal.previousDir(), entry.Name)
			body, err := os.ReadFile(prev) //nolint:gosec
			if err != nil {
				if rollbackErr == nil {
					rollbackErr = err
				}
				continue
			}
			mode := os.FileMode(entry.Mode)
			if mode == 0 {
				mode = 0600
			}
			if err := writeFileDurable(destination, body, mode); err != nil && rollbackErr == nil {
				rollbackErr = err
			}
			continue
		}
		// New install or removal of a file that did not exist pre-restore.
		if err := os.Remove(destination); err != nil && !os.IsNotExist(err) && rollbackErr == nil {
			rollbackErr = err
		}
	}
	if state.CreatedModsDir {
		if err := os.Remove(modsDir); err != nil && !os.IsNotExist(err) && rollbackErr == nil {
			rollbackErr = err
		}
	}
	return rollbackErr
}

// recoverIncompleteModuleRestore rolls back an interrupted module restore if a
// durable journal remains from a previous process. Safe to call on every boot.
func recoverIncompleteModuleRestore() error {
	return withModuleTransaction(func() error {
		return recoverIncompleteModuleRestoreLocked()
	})
}

func recoverIncompleteModuleRestoreLocked() error {
	journal := openRestoreJournal()
	if !journal.exists() {
		// Leftover empty dir from a partial remove is harmless.
		_ = os.RemoveAll(journal.dir)
		return nil
	}
	state, err := journal.readState()
	if err != nil {
		// Unreadable journal: refuse to guess; surface the error so operators notice.
		return fmt.Errorf("read restore journal: %w", err)
	}
	switch state.Phase {
	case restorePhaseDBApplied:
		// Forward commit completed; only journal cleanup was interrupted.
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear completed restore journal: %w", err)
		}
		return nil
	case restorePhasePrepared:
		// Live state never mutated (or only journal prep). Discard.
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear prepared restore journal: %w", err)
		}
		return nil
	case restorePhaseApplying, restorePhaseFilesApplied, restorePhaseDBApplying, "":
		// applying / files_applied: DB pre-restore → safe FS rollback.
		// db_applying / empty: DB uncertain → prefer safe FS rollback (residual:
		// if Reset already committed, boot may see FS-old + DB-new).
		if err := rollbackRestoreJournalState(state, journal); err != nil {
			return fmt.Errorf("rollback incomplete restore (phase %q): %w", state.Phase, err)
		}
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear rolled-back restore journal: %w", err)
		}
		return nil
	default:
		// Unknown phase: treat as incomplete / DB-uncertain and prefer FS rollback.
		if err := rollbackRestoreJournalState(state, journal); err != nil {
			return fmt.Errorf("rollback restore journal phase %q: %w", state.Phase, err)
		}
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear restore journal phase %q: %w", state.Phase, err)
		}
		return nil
	}
}
