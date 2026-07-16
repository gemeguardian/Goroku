package modules

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"goroku/goroku"
)

// Restore journal dual-commit protocol (M1.1):
//
// applyRestore cannot make filesystem module sources and Database.Reset jointly
// crash-atomic with a single rename across two stores. Instead we use a durable
// dual-commit journal plus a restore ID stamped into the DB on Reset:
//
//  1. Stage all intended module bodies under journal/staged/ (kept until finish).
//  2. Snapshot previous live bodies under journal/previous/.
//  3. Write journal {prepared, restore_id, payload_hash, intended paths}.
//  4. Apply live FS from staged (copy/write, not consume-rename) → files_applied.
//  5. mark db_applying, inject restore_id (+ payload_hash) into Reset payload.
//  6. Database.Reset (single-file atomic temp+rename inside DB layer).
//  7. db_applied → remove journal.
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
//	                Crash + DB carries this journal's restore_id → forward commit
//	                (re-apply FS from staged if needed, drop journal).
//	                Crash + DB lacks restore_id → prefer safe FS rollback.
//	db_applied    – FS and DB both match backup; journal not yet removed.
//	                Crash → drop journal (forward commit).
//
// Residual (not fully jointly atomic without multi-store coordinator):
//   - If recovery cannot read the live Database (nil / not initialized) while the
//     phase is db_applying, recovery still prefers FS rollback and may yield
//     FS-old + DB-new when Reset already renamed on disk.
//   - Same class as ErrDatabaseCommitUncertain when no durable restore_id is
//     observable yet (Reset renamed but process died before any reader can see it
//     is not distinguishable from "Reset never started" without the ID stamp).
//   - True single-rename joint atomicity would need a DB API that stages a
//     candidate file and renames only after FS commit is durable (or a real
//     multi-store coordinator).
//
// Ambiguous phases (empty, unknown): always prefer safe FS rollback over
// forward commit unless a matching restore_id proves DB forward-commit.

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

	// Stamped into Database.Reset payload so crash recovery can detect whether
	// this restore's DB commit already landed.
	restoreDBOwner   = "goroku.restore"
	restoreDBIDKey   = "restore_id"
	restoreDBHashKey = "fs_payload_hash"
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
	RestoreID      string                `json:"restore_id,omitempty"`
	PayloadHash    string                `json:"payload_hash,omitempty"`
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

func newRestoreID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// computeRestorePayloadHash is a deterministic digest of the intended post-restore
// module tree (install bodies + removals). Used for boot FS verification.
func computeRestorePayloadHash(entries []restoreJournalEntry, stagedSources map[string][]byte) string {
	h := sha256.New()
	for _, entry := range entries {
		_, _ = h.Write([]byte(entry.Name))
		_, _ = h.Write([]byte{0})
		if entry.Install {
			_, _ = h.Write(stagedSources[entry.Name])
		} else {
			_, _ = h.Write([]byte{'-'})
		}
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stampRestoreCommitMetadata injects this restore's ID (and optional FS hash)
// into the Database.Reset payload so recovery can detect a completed DB commit.
func stampRestoreCommitMetadata(data map[string]map[string]any, restoreID, payloadHash string) map[string]map[string]any {
	if data == nil {
		data = make(map[string]map[string]any)
	}
	owner := data[restoreDBOwner]
	if owner == nil {
		owner = make(map[string]any)
		data[restoreDBOwner] = owner
	}
	owner[restoreDBIDKey] = restoreID
	if payloadHash != "" {
		owner[restoreDBHashKey] = payloadHash
	}
	return data
}

func restoreIDFromDatabase(db *goroku.Database) string {
	if db == nil {
		return ""
	}
	return db.GetString(restoreDBOwner, restoreDBIDKey, "")
}

func restorePayloadHashFromDatabase(db *goroku.Database) string {
	if db == nil {
		return ""
	}
	return db.GetString(restoreDBOwner, restoreDBHashKey, "")
}

// databaseHasRestoreCommit reports whether the live DB already carries this
// journal's restore_id (Reset completed for this dual-commit attempt).
func databaseHasRestoreCommit(db *goroku.Database, state *restoreJournalState) bool {
	if db == nil || state == nil || state.RestoreID == "" {
		return false
	}
	return restoreIDFromDatabase(db) == state.RestoreID
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
	restoreID, err := newRestoreID()
	if err != nil {
		_ = j.remove()
		return err
	}
	state := &restoreJournalState{
		Phase:          restorePhasePrepared,
		ModsDir:        modsDir,
		CreatedModsDir: createdModsDir,
		Entries:        entries,
		RestoreID:      restoreID,
		PayloadHash:    computeRestorePayloadHash(entries, stagedSources),
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
// db_applying is dangerous (boot would roll FS back unless restore_id proves DB
// forward-commit). Prefer durable db_applied, then clear; if phase advance fails,
// still attempt remove so no journal remains.
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

// forwardApplyRestoreJournalState re-applies the intended post-restore module
// tree from durable staged/ copies. Used when recovery proves DB already
// committed this restore_id (FS must match backup, not previous/).
func forwardApplyRestoreJournalState(state *restoreJournalState, journal *restoreJournal) error {
	var applyErr error
	modsDir := state.ModsDir
	if modsDir == "" {
		modsDir = runtimeModuleSourceDir()
	}
	if err := os.MkdirAll(modsDir, 0700); err != nil {
		return err
	}
	for _, entry := range state.Entries {
		destination := filepath.Join(modsDir, entry.Name)
		if !entry.Install {
			if err := os.Remove(destination); err != nil && !os.IsNotExist(err) && applyErr == nil {
				applyErr = err
			}
			continue
		}
		body, err := os.ReadFile(filepath.Join(journal.stagedDir(), entry.Name)) //nolint:gosec
		if err != nil {
			if applyErr == nil {
				applyErr = err
			}
			continue
		}
		if err := writeFileDurable(destination, body, 0600); err != nil && applyErr == nil {
			applyErr = err
		}
	}
	return applyErr
}

// recoverIncompleteModuleRestore rolls back or forward-commits an interrupted
// module restore if a durable journal remains. Safe to call on every boot.
// Pass the live Database when available so db_applying can detect a completed
// Reset via restore_id and avoid FS-old/DB-new divergence.
func recoverIncompleteModuleRestore(db *goroku.Database) error {
	return withModuleTransaction(func() error {
		return recoverIncompleteModuleRestoreLocked(db)
	})
}

func recoverIncompleteModuleRestoreLocked(db *goroku.Database) error {
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
	case restorePhaseDBApplying:
		// DB may already carry this restore_id (Reset committed). Prefer forward
		// commit so we do not invent FS-old + DB-new.
		if databaseHasRestoreCommit(db, state) {
			if err := forwardApplyRestoreJournalState(state, journal); err != nil {
				return fmt.Errorf("forward-apply restore after db commit (phase %q): %w", state.Phase, err)
			}
			if err := journal.remove(); err != nil {
				return fmt.Errorf("clear forward-committed restore journal: %w", err)
			}
			return nil
		}
		if err := rollbackRestoreJournalState(state, journal); err != nil {
			return fmt.Errorf("rollback incomplete restore (phase %q): %w", state.Phase, err)
		}
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear rolled-back restore journal: %w", err)
		}
		return nil
	case restorePhaseApplying, restorePhaseFilesApplied, "":
		// applying / files_applied: DB pre-restore → safe FS rollback.
		// empty: treat as incomplete / uncertain → prefer FS rollback unless
		// restore_id proves DB already advanced (defensive).
		if state.Phase == "" && databaseHasRestoreCommit(db, state) {
			if err := forwardApplyRestoreJournalState(state, journal); err != nil {
				return fmt.Errorf("forward-apply restore after db commit (phase %q): %w", state.Phase, err)
			}
			if err := journal.remove(); err != nil {
				return fmt.Errorf("clear forward-committed restore journal: %w", err)
			}
			return nil
		}
		if err := rollbackRestoreJournalState(state, journal); err != nil {
			return fmt.Errorf("rollback incomplete restore (phase %q): %w", state.Phase, err)
		}
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear rolled-back restore journal: %w", err)
		}
		return nil
	default:
		// Unknown phase: if restore_id matches, forward; else prefer FS rollback.
		if databaseHasRestoreCommit(db, state) {
			if err := forwardApplyRestoreJournalState(state, journal); err != nil {
				return fmt.Errorf("forward-apply restore journal phase %q: %w", state.Phase, err)
			}
			if err := journal.remove(); err != nil {
				return fmt.Errorf("clear restore journal phase %q: %w", state.Phase, err)
			}
			return nil
		}
		if err := rollbackRestoreJournalState(state, journal); err != nil {
			return fmt.Errorf("rollback restore journal phase %q: %w", state.Phase, err)
		}
		if err := journal.remove(); err != nil {
			return fmt.Errorf("clear restore journal phase %q: %w", state.Phase, err)
		}
		return nil
	}
}
