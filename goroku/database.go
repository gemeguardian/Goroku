package goroku

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"goroku/goroku/utils"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var (
	ErrDatabaseClosed          = errors.New("database closed")
	ErrDatabaseNotInitialized  = errors.New("database not initialized")
	ErrDatabaseWriteProtected  = errors.New("database write protected")
	ErrDatabaseInvalidValue    = errors.New("invalid database value")
	ErrDatabasePersistence     = errors.New("database persistence failure")
	ErrDatabaseCommitUncertain = errors.New("database commit completed with uncertain durability")
	ErrDatabaseNoRevision      = errors.New("database has no revision")
	ErrDatabaseCorrupt         = errors.New("database file corrupt")
	errAtomicWriteCommitted    = errors.New("atomic file rename committed")

	dbProtectedOwners = map[string]bool{
		"GorokuPluginSecurity": true,
	}
	dbAllowedWriters = map[string]bool{
		"goroku/goroku/modules.(*GorokuPluginSecurity).UnexternalCmd": true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).ExternalCmd":   true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).AllowmodCmd":   true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).DenymodCmd":    true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).TrustmodCmd":   true,
		// Content-digest trust writes (called from TrustmodCmd/AllowmodCmd).
		"goroku/goroku/modules.trustContentDigest": true,
		// Content-digest untrust writes (called from TrustmodCmd/ExternalCmd/DenymodCmd).
		"goroku/goroku/modules.untrustContentDigest": true,
	}
)

// DatabaseError adds operation and storage context while preserving errors.Is/errors.As.
type DatabaseError struct {
	Operation string
	Owner     string
	Key       string
	Backend   string
	// Committed means the candidate is already the current file and was
	// published in memory. Callers must not roll back as if the write failed.
	Committed bool
	Err       error
}

func (e *DatabaseError) Error() string {
	location := e.Owner
	if e.Key != "" {
		location += "/" + e.Key
	}
	if location != "" {
		location = " " + location
	}
	backend := ""
	if e.Backend != "" {
		backend = " (" + e.Backend + ")"
	}
	return fmt.Sprintf("database %s%s%s: %v", e.Operation, location, backend, e.Err)
}

func (e *DatabaseError) Unwrap() error { return e.Err }

func databaseError(operation, owner, key, backend string, sentinel, cause error) error {
	err := sentinel
	if cause != nil {
		err = errors.Join(sentinel, cause)
	}
	return &DatabaseError{Operation: operation, Owner: owner, Key: key, Backend: backend, Err: err}
}

func committedDatabaseError(operation, owner, key, backend string, cause error) error {
	return &DatabaseError{
		Operation: operation,
		Owner:     owner,
		Key:       key,
		Backend:   backend,
		Committed: true,
		Err:       errors.Join(ErrDatabasePersistence, ErrDatabaseCommitUncertain, cause),
	}
}

// Database is the public document + asset façade.
// DocumentStore holds KV/JSON/Redis state; AssetRepository handles Telegram assets.
type Database struct {
	DocumentStore
	assets AssetRepository
}

func NewDatabase(tgID int64) *Database {
	db := &Database{
		DocumentStore: DocumentStore{
			tgID:       tgID,
			data:       make(map[string]map[string]any),
			revisions:  make([]map[string]map[string]any, 0),
			writeLocal: writeFileAtomic,
		},
	}
	db.assets.store = &db.DocumentStore
	return db
}

// AttachRuntime wires narrow loader/config/asset hooks after the module loader exists.
// Replaces the former Database.client *CustomTelegramClient back-reference.
func (db *Database) AttachRuntime(names ModuleNameResolver, reloader ConfigReloader, transport AssetTransport) {
	if db == nil {
		return
	}
	db.setModuleNameResolver(names)
	db.setConfigReloader(reloader)
	db.assets.store = &db.DocumentStore
	db.assets.SetTransport(transport)
}

func (db *Database) Init(redisURI string) error {
	dbFile := filepath.Join(BaseDir, fmt.Sprintf("config-%d.json", db.tgID))
	var redisClient *redis.Client
	if redisURI != "" {
		opt, err := redis.ParseURL(redisURI)
		if err != nil {
			return fmt.Errorf("parse Redis URL: %w", err)
		}
		opt.ContextTimeoutEnabled = true
		redisClient = redis.NewClient(opt)
	}

	data, status := db.loadLocal(dbFile)
	fromRedis := false
	redisDirty := false
	lastRedisSave := int64(0)
	if status.valid && redisClient != nil {
		bytes, err := json.Marshal(data)
		if err != nil {
			_ = redisClient.Close()
			return fmt.Errorf("marshal local database for Redis: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = redisClient.Set(ctx, fmt.Sprintf("%d", db.tgID), bytes, 0).Err()
		cancel()
		if err != nil {
			redisDirty = true
			L().Info("Database Redis startup sync failed: {0}", zap.Any("arg0", err))
		} else {
			lastRedisSave = time.Now().Unix()
		}
	} else if !status.valid {
		if status.primaryCorrupt {
			var redisOK bool
			data, redisOK = db.readRedis(redisClient)
			if !redisOK {
				if redisClient != nil {
					_ = redisClient.Close()
				}
				return databaseError("init", "", "", "local", ErrDatabaseCorrupt,
					fmt.Errorf("primary %q is corrupt and last-valid/Redis recovery failed", dbFile))
			}
			fromRedis = true
			redisLabel := "redis"
			if redisBytes, mErr := json.Marshal(data); mErr == nil {
				sum := sha256.Sum256(redisBytes)
				redisLabel = fmt.Sprintf("redis size=%d sha256=%x", len(redisBytes), sum[:8])
			}
			L().Warn("Database primary corrupt; recovered from Redis mirror after last-valid failure",
				zap.String("path", dbFile),
				zap.String("backup", lastValidPath(dbFile)),
				zap.String("generation", redisLabel))
		} else {
			data, fromRedis = db.readRedis(redisClient)
		}
	}

	if fromRedis {
		bytes, err := json.MarshalIndent(data, "", "    ")
		if err != nil {
			_ = redisClient.Close()
			return fmt.Errorf("marshal Redis database for local fallback: %w", err)
		}
		if err := writeFileAtomic(dbFile, bytes); err != nil && !errors.Is(err, errAtomicWriteCommitted) {
			_ = redisClient.Close()
			return fmt.Errorf("update local database fallback: %w", err)
		}
	} else {
		utils.SecureFile(dbFile)
	}

	db.mu.Lock()
	if db.closing || db.closed {
		db.mu.Unlock()
		if redisClient != nil {
			_ = redisClient.Close()
		}
		return databaseError("init", "", "", "", ErrDatabaseClosed, nil)
	}
	db.dbFile = dbFile
	db.redisClient = redisClient
	db.data = data
	db.redisDirty = redisDirty
	db.lastRedisSave = lastRedisSave
	db.initialized = true
	if redisClient != nil {
		ctx, cancel := context.WithCancel(context.Background())
		db.flushCancel = cancel
		db.flushDone = make(chan struct{})
		go db.redisFlushLoop(ctx, db.flushDone)
	}
	db.mu.Unlock()
	return nil
}

func (db *Database) redisFlushLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := db.flushRedis(ctx); err != nil && ctx.Err() == nil {
				L().Info("Database Redis flush failed: {0}", zap.Any("arg0", err))
			}
		}
	}
}

type localLoadStatus struct {
	valid          bool
	primaryMissing bool
	primaryCorrupt bool
	recovered      bool
	source         string
	recoveryLabel  string
}

func lastValidPath(path string) string {
	return path + ".last-valid"
}

func parseDatabaseContent(content []byte) (map[string]map[string]any, error) {
	// Convert legacy names if present in the string (similar to python's regex replacement)
	dbStr := string(content)
	dbStr = strings.ReplaceAll(dbStr, "hikka.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "legacy.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "heroku.", "goroku.")

	var parsed map[string]map[string]any
	if err := json.Unmarshal([]byte(dbStr), &parsed); err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, errors.New("database JSON is null")
	}
	return parsed, nil
}

func isRetainedSnapshotValid(content []byte) bool {
	var obj map[string]any
	if err := json.Unmarshal(content, &obj); err != nil {
		return false
	}
	return obj != nil
}

func fileGenerationLabel(path string, content []byte) string {
	sum := sha256.Sum256(content)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("size=%d sha256=%x", len(content), sum[:8])
	}
	return fmt.Sprintf("mtime=%s size=%d sha256=%x",
		info.ModTime().UTC().Format(time.RFC3339Nano), info.Size(), sum[:8])
}

func (db *Database) readLastValid(dbFile string) (map[string]map[string]any, bool, string) {
	path := lastValidPath(dbFile)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, ""
	}
	parsed, err := parseDatabaseContent(content)
	if err != nil {
		return nil, false, ""
	}
	return parsed, true, fileGenerationLabel(path, content)
}

func (db *Database) repairLocal(dbFile string, data map[string]map[string]any) {
	bytes, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		L().Warn("Database recovery repair marshal failed", zap.String("path", dbFile), zap.Error(err))
		return
	}
	if err := writeFileAtomic(dbFile, bytes); err != nil && !errors.Is(err, errAtomicWriteCommitted) {
		L().Warn("Database recovery repair write failed", zap.String("path", dbFile), zap.Error(err))
	}
}

func (db *Database) loadLocal(dbFile string) (map[string]map[string]any, localLoadStatus) {
	content, err := os.ReadFile(dbFile)
	if err != nil {
		if os.IsNotExist(err) {
			if data, ok, label := db.readLastValid(dbFile); ok {
				L().Warn("Database primary missing; recovered from last-valid copy",
					zap.String("path", dbFile),
					zap.String("backup", lastValidPath(dbFile)),
					zap.String("generation", label))
				db.repairLocal(dbFile, data)
				return data, localLoadStatus{
					valid:          true,
					primaryMissing: true,
					recovered:      true,
					source:         "last-valid",
					recoveryLabel:  label,
				}
			}
			return make(map[string]map[string]any), localLoadStatus{primaryMissing: true}
		}
		L().Info("Error reading database file: {0}", zap.Any("arg0", err))
		if data, ok, label := db.readLastValid(dbFile); ok {
			L().Warn("Database primary unreadable; recovered from last-valid copy",
				zap.String("path", dbFile),
				zap.String("backup", lastValidPath(dbFile)),
				zap.String("generation", label),
				zap.Error(err))
			db.repairLocal(dbFile, data)
			return data, localLoadStatus{
				valid:          true,
				primaryCorrupt: true,
				recovered:      true,
				source:         "last-valid",
				recoveryLabel:  label,
			}
		}
		return make(map[string]map[string]any), localLoadStatus{primaryCorrupt: true}
	}

	parsed, err := parseDatabaseContent(content)
	if err == nil {
		return parsed, localLoadStatus{valid: true, source: "primary"}
	}

	L().Info("Database read failed! Error: {0}", zap.Any("arg0", err))
	if data, ok, label := db.readLastValid(dbFile); ok {
		L().Warn("Database recovered from last-valid copy",
			zap.String("path", dbFile),
			zap.String("backup", lastValidPath(dbFile)),
			zap.String("generation", label),
			zap.Error(err))
		db.repairLocal(dbFile, data)
		return data, localLoadStatus{
			valid:          true,
			primaryCorrupt: true,
			recovered:      true,
			source:         "last-valid",
			recoveryLabel:  label,
		}
	}
	return make(map[string]map[string]any), localLoadStatus{primaryCorrupt: true}
}

func (db *Database) readLocal(dbFile string) (map[string]map[string]any, bool) {
	data, status := db.loadLocal(dbFile)
	return data, status.valid
}

func (db *Database) readRedis(redisClient *redis.Client) (map[string]map[string]any, bool) {
	if redisClient == nil {
		return make(map[string]map[string]any), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	val, err := redisClient.Get(ctx, fmt.Sprintf("%d", db.tgID)).Result()
	if err == nil {
		var parsed map[string]map[string]any
		if err := json.Unmarshal([]byte(val), &parsed); err == nil && parsed != nil {
			return parsed, true
		}
	}
	log.Println("Error reading Redis database, starting with empty database")
	return make(map[string]map[string]any), false
}

func (db *Database) Save() error {
	return db.saveContext(context.Background(), false)
}

// SaveContext is intentionally separate from Save rather than a variadic Save.
// Both concrete methods are retained for compatibility with ignored source users.
func (db *Database) SaveContext(ctx context.Context) error {
	return db.saveContext(ctx, true)
}

func (db *Database) saveContext(ctx context.Context, reportDurabilityWarning bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !db.lockPersistence(ctx) {
		return ctx.Err()
	}
	defer db.persistMu.Unlock()

	db.mu.RLock()
	if err := db.stateError("save", "", ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	candidate := db.deepCopy(db.data)
	db.mu.RUnlock()
	if err := db.commitCandidate("save", "", "", candidate, false); err != nil {
		return err
	}
	if reportDurabilityWarning {
		return db.DurabilityWarning()
	}
	return nil
}

// commitCandidate persists a detached state before making it visible.
// db.persistMu must be held by the caller.
func (db *Database) commitCandidate(operation, owner, key string, candidate map[string]map[string]any, recordRevision bool) error {
	processDBAutofix(candidate)
	bytes, err := json.MarshalIndent(candidate, "", "    ")
	if err != nil {
		return databaseError(operation, owner, key, "local", ErrDatabaseInvalidValue, err)
	}

	db.mu.RLock()
	if err := db.stateError(operation, owner, key); err != nil {
		db.mu.RUnlock()
		return err
	}
	dbFile := db.dbFile
	var previous map[string]map[string]any
	if recordRevision {
		previous = db.deepCopy(db.data)
	}
	db.mu.RUnlock()
	persistErr := db.writeLocal(dbFile, bytes)
	committedWithWarning := errors.Is(persistErr, errAtomicWriteCommitted)
	if persistErr != nil && !committedWithWarning {
		return databaseError(operation, owner, key, "local", ErrDatabasePersistence, persistErr)
	}

	now := time.Now().Unix()
	db.mu.Lock()
	if recordRevision {
		db.revisions = append(db.revisions, previous)
		if len(db.revisions) > 15 {
			db.revisions = db.revisions[len(db.revisions)-15:]
		}
	}
	db.data = candidate
	db.generation++
	generation := db.generation
	if committedWithWarning {
		db.durabilityErr = committedDatabaseError(operation, owner, key, "local", persistErr)
		db.durabilityGen = generation
	} else {
		// This write durably persisted the candidate that is being published as
		// the current generation, so any warning for an older generation is stale.
		db.durabilityErr = nil
		db.durabilityGen = 0
	}
	redisClient := db.redisClient
	flushRedis := redisClient != nil && now-db.lastRedisSave >= 5
	if redisClient != nil {
		db.redisDirty = true
	}
	db.mu.Unlock()

	if flushRedis {
		redisBytes, marshalErr := json.Marshal(candidate)
		if marshalErr == nil {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			redisErr := redisClient.Set(flushCtx, fmt.Sprintf("%d", db.tgID), redisBytes, 0).Err()
			cancel()
			if redisErr == nil {
				db.markRedisFlushed(generation)
			} else {
				db.recordRedisError(operation, owner, key, redisErr)
			}
		}
	}
	if committedWithWarning {
		L().Warn("Database logical commit completed with uncertain durability",
			zap.String("operation", operation),
			zap.String("owner", owner),
			zap.String("key", key),
			zap.Uint64("generation", generation),
			zap.Error(db.DurabilityWarning()))
	}
	return nil
}

// DurabilityWarning returns the unresolved warning for the current database
// generation. Ordinary mutations report logical success after the rename
// commit point; callers that need persistence diagnostics can check this or use
// SaveContext/Close as a finalization boundary.
func (db *Database) DurabilityWarning() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.durabilityGen != db.generation {
		return nil
	}
	return db.durabilityErr
}

// stateError requires db.mu to be held for reading or writing.
func (db *Database) stateError(operation, owner, key string) error {
	if db.closing || db.closed {
		return databaseError(operation, owner, key, "", ErrDatabaseClosed, nil)
	}
	if !db.initialized || db.dbFile == "" {
		return databaseError(operation, owner, key, "", ErrDatabaseNotInitialized, nil)
	}
	return nil
}

func (db *Database) recordRedisError(operation, owner, key string, cause error) {
	err := databaseError(operation, owner, key, "redis", ErrDatabasePersistence, cause)
	db.mu.Lock()
	db.lastRedisErr = err
	db.mu.Unlock()
	L().Info("Database Redis mirror failed: {0}", zap.Any("arg0", err))
}

func (db *Database) flushRedis(ctx context.Context) error {
	if !db.lockPersistence(ctx) {
		return ctx.Err()
	}
	defer db.persistMu.Unlock()
	return db.flushRedisInner(ctx)
}

// flushRedisInner flushes while db.persistMu is held.
func (db *Database) flushRedisInner(ctx context.Context) error {
	db.mu.RLock()
	if db.redisClient == nil || !db.redisDirty {
		db.mu.RUnlock()
		return nil
	}
	redisClient := db.redisClient
	generation := db.generation
	snapshot := db.deepCopy(db.data)
	db.mu.RUnlock()

	bytes, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := redisClient.Set(flushCtx, fmt.Sprintf("%d", db.tgID), bytes, 0).Err(); err != nil {
		db.recordRedisError("flush", "", "", err)
		return err
	}
	db.markRedisFlushed(generation)
	return nil
}

func (db *Database) lockPersistence(ctx context.Context) bool {
	for {
		if db.persistMu.TryLock() {
			return true
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (db *Database) markRedisFlushed(generation uint64) {
	db.mu.Lock()
	db.lastRedisSave = time.Now().Unix()
	if db.generation == generation {
		db.redisDirty = false
		db.lastRedisErr = nil
	}
	db.mu.Unlock()
}

type atomicFileOps struct {
	openDir  func(string) (*os.File, error)
	syncDir  func(*os.File) error
	closeDir func(*os.File) error
	remove   func(string) error
	rename   func(string, string) error
}

var defaultAtomicFileOps = atomicFileOps{
	openDir:  os.Open,
	syncDir:  (*os.File).Sync,
	closeDir: (*os.File).Close,
	remove:   os.Remove,
	rename:   os.Rename,
}

func syncDirectory(dir string, ops atomicFileOps) error {
	dirFile, err := ops.openDir(dir)
	if err != nil {
		return err
	}
	syncErr := ops.syncDir(dirFile)
	closeErr := ops.closeDir(dirFile)
	return errors.Join(syncErr, closeErr)
}

func writeFileAtomic(path string, data []byte) error {
	return writeFileAtomicWithOps(path, data, defaultAtomicFileOps)
}

// copyFileContents copies src to dst via a same-directory temp + rename so a
// failed write never truncates or removes an existing destination (e.g. last-valid).
func copyFileContents(src, dst string) error {
	return copyFileContentsWithOps(src, dst, defaultAtomicFileOps)
}

func copyFileContentsWithOps(src, dst string, ops atomicFileOps) (err error) {
	dir := filepath.Dir(dst)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dir, ".goroku-db-copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = ops.remove(tmpName)
		}
	}()

	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = io.Copy(tmp, in); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = ops.rename(tmpName, dst); err != nil {
		return err
	}
	return nil
}

// retainLastValid promotes a crash-window snapshot of the previous primary to the
// durable path+".last-valid" sibling. Invalid previous content must not clobber an
// existing good last-valid copy. Fallback install also uses temp+rename so a
// failed copy cannot truncate the live last-valid sibling.
func retainLastValid(path, backupName string, ops atomicFileOps) error {
	content, err := os.ReadFile(backupName)
	if err != nil {
		return err
	}
	if !isRetainedSnapshotValid(content) {
		if rmErr := ops.remove(backupName); rmErr != nil {
			return fmt.Errorf("remove invalid previous snapshot: %w", rmErr)
		}
		if syncErr := syncDirectory(filepath.Dir(path), ops); syncErr != nil {
			return fmt.Errorf("sync after dropping invalid snapshot: %w", syncErr)
		}
		return nil
	}

	lastValid := lastValidPath(path)
	if err := ops.rename(backupName, lastValid); err != nil {
		if copyErr := copyFileContentsWithOps(backupName, lastValid, ops); copyErr != nil {
			return errors.Join(err, copyErr)
		}
		if rmErr := ops.remove(backupName); rmErr != nil {
			return fmt.Errorf("remove ephemeral snapshot after copy: %w", rmErr)
		}
	}
	if chmodErr := os.Chmod(lastValid, 0600); chmodErr != nil {
		return fmt.Errorf("chmod last-valid: %w", chmodErr)
	}
	if syncErr := syncDirectory(filepath.Dir(path), ops); syncErr != nil {
		return fmt.Errorf("sync last-valid retention: %w", syncErr)
	}
	return nil
}

func writeFileAtomicWithOps(path string, data []byte, ops atomicFileOps) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".goroku-db-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = ops.remove(tmpName)
		}
	}()

	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}

	backup, err := os.CreateTemp(dir, ".goroku-db-backup-*")
	if err != nil {
		return err
	}
	backupName := backup.Name()
	if err = backup.Close(); err != nil {
		_ = ops.remove(backupName)
		return err
	}
	if err = ops.remove(backupName); err != nil {
		return err
	}

	hadPrevious := false
	if linkErr := os.Link(path, backupName); linkErr == nil {
		hadPrevious = true
		if err = syncDirectory(dir, ops); err != nil {
			_ = ops.remove(backupName)
			return err
		}
	} else if !os.IsNotExist(linkErr) {
		// Some filesystems reject hardlinks; fall back to a full copy so the
		// write can still proceed and last-valid retention remains best-effort.
		if copyErr := copyFileContents(path, backupName); copyErr == nil {
			hadPrevious = true
			if err = syncDirectory(dir, ops); err != nil {
				_ = ops.remove(backupName)
				return err
			}
		}
	}
	defer func() {
		if err != nil && !errors.Is(err, errAtomicWriteCommitted) {
			_ = ops.remove(backupName)
		}
	}()

	if err = ops.rename(tmpName, path); err != nil {
		return err
	}
	// Rename is the commit point. Never attempt rollback after this point: a
	// failed rollback would create a second ambiguity about which file is live.
	var postCommitErr error
	if syncErr := syncDirectory(dir, ops); syncErr != nil {
		postCommitErr = errors.Join(postCommitErr, fmt.Errorf("sync committed directory: %w", syncErr))
	}
	if hadPrevious {
		if retainErr := retainLastValid(path, backupName, ops); retainErr != nil {
			postCommitErr = errors.Join(postCommitErr, fmt.Errorf("retain last-valid database copy: %w", retainErr))
			_ = ops.remove(backupName)
		}
	}
	if postCommitErr != nil {
		return errors.Join(errAtomicWriteCommitted, postCommitErr)
	}
	return nil
}

// Close stops background flushing and closes the Redis client.
func (db *Database) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	db.mu.Lock()
	if db.closing || db.closed {
		done := db.closeDone
		db.mu.Unlock()
		select {
		case <-done:
			db.mu.RLock()
			err := db.closeErr
			db.mu.RUnlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	db.closing = true
	db.closeDone = make(chan struct{})
	closeDone := db.closeDone
	cancel := db.flushCancel
	done := db.flushDone
	db.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	go db.finishClose(context.Background(), done, closeDone)

	select {
	case <-closeDone:
		db.mu.RLock()
		err := db.closeErr
		db.mu.RUnlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (db *Database) finishClose(ctx context.Context, flushDone, closeDone chan struct{}) {
	var result error
	if flushDone != nil {
		select {
		case <-flushDone:
		case <-ctx.Done():
			result = ctx.Err()
		}
	}

	db.persistMu.Lock()
	if result == nil {
		if err := db.flushRedisInner(ctx); err != nil {
			db.mu.RLock()
			result = db.lastRedisErr
			db.mu.RUnlock()
			if result == nil {
				result = err
			}
		}
	}
	db.mu.RLock()
	redisClient := db.redisClient
	db.mu.RUnlock()
	if redisClient != nil {
		if err := redisClient.Close(); result == nil {
			result = err
		}
	}
	db.mu.RLock()
	durabilityErr := db.durabilityErr
	if db.durabilityGen != db.generation {
		durabilityErr = nil
	}
	db.mu.RUnlock()
	result = errors.Join(result, durabilityErr)

	db.mu.Lock()
	db.redisClient = nil
	db.closed = true
	db.closing = false
	db.closeErr = result
	close(closeDone)
	db.mu.Unlock()
	db.persistMu.Unlock()
}

// Rollback durably restores the database from the latest revision.
func (db *Database) Rollback() error {
	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	db.mu.RLock()
	if err := db.stateError("rollback", "", ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	if len(db.revisions) == 0 {
		db.mu.RUnlock()
		return databaseError("rollback", "", "", "", ErrDatabaseNoRevision, nil)
	}
	rev := db.deepCopy(db.revisions[len(db.revisions)-1])
	db.mu.RUnlock()
	err := db.commitCandidate("rollback", "", "", rev, false)
	if err != nil {
		return err
	}
	db.mu.Lock()
	db.revisions = db.revisions[:len(db.revisions)-1]
	db.mu.Unlock()
	log.Println("Database rollback: restored from revision")
	return nil
}

func processDBAutofix(data map[string]map[string]any) {
	for modName, keys := range data {
		if keys == nil {
			delete(data, modName)
			continue
		}
	}
}

func (db *Database) normalizeOwner(owner string) string {
	// 1. Try exact match from db.data first (with RLock)
	db.mu.RLock()
	_, exactExists := db.data[owner]
	db.mu.RUnlock()
	if exactExists {
		return owner
	}

	// 2. Try case-insensitive match against registered modules
	if resolver := db.moduleNameResolver(); resolver != nil {
		for _, name := range resolver.ModuleNames() {
			if strings.EqualFold(name, owner) {
				return name
			}
		}
	}

	// 3. Fallback: try case-insensitive match against existing keys in db.data (with RLock)
	db.mu.RLock()
	defer db.mu.RUnlock()
	for k := range db.data {
		if strings.EqualFold(k, owner) {
			return k
		}
	}

	return owner
}

func (db *Database) Get(owner, key string, defaultValue any) (any, error) {
	owner = db.normalizeOwner(owner)
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateError("get", owner, key); err != nil {
		return deepCopyValue(defaultValue), err
	}

	if mod, ok := db.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return deepCopyValue(val), nil
		}
	}
	return deepCopyValue(defaultValue), nil
}

// GetValue returns the stored value or default without an error.
// Deprecated: use Get() when lifecycle diagnostics matter. GetValue and the
// typed getters intentionally suppress lifecycle checks for compatibility
// with callers that populate an in-memory Database before Init.
func (db *Database) GetValue(owner, key string, defaultValue any) any {
	return db.getValueCompat(owner, key, defaultValue)

}

func (db *Database) getValueCompat(owner, key string, defaultValue any) any {
	owner = db.normalizeOwner(owner)
	db.mu.RLock()
	defer db.mu.RUnlock()
	if mod, ok := db.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return deepCopyValue(val)
		}
	}
	return deepCopyValue(defaultValue)
}

// GetString returns the stored string value or the default.
func (db *Database) GetString(owner, key, def string) string {
	val := db.getValueCompat(owner, key, def)
	if s, ok := val.(string); ok {
		return s
	}
	return def
}

// GetInt64 returns the stored integer value or the default.
// It normalises JSON float64 / json.Number values back to int64.
func (db *Database) GetInt64(owner, key string, def int64) int64 {
	val := db.getValueCompat(owner, key, def)
	return asInt64(val, def)
}

// GetInt returns the stored int or the default.
func (db *Database) GetInt(owner, key string, def int) int {
	val := db.getValueCompat(owner, key, def)
	return int(asInt64(val, int64(def)))
}

// GetBool returns the stored boolean value or the default.
func (db *Database) GetBool(owner, key string, def bool) bool {
	val := db.getValueCompat(owner, key, def)
	if b, ok := val.(bool); ok {
		return b
	}
	return def
}

// GetStringSlice returns the stored []string or the default.
// It converts []any produced by JSON unmarshalling.
func (db *Database) GetStringSlice(owner, key string, def []string) []string {
	val := db.getValueCompat(owner, key, def)
	switch v := val.(type) {
	case []string:
		res := make([]string, len(v))
		copy(res, v)
		return res
	case []any:
		res := make([]string, 0, len(v))
		for _, item := range v {
			res = append(res, fmt.Sprintf("%v", item))
		}
		return res
	}
	if def == nil {
		return []string{}
	}
	res := make([]string, len(def))
	copy(res, def)
	return res
}

// GetInt64Slice returns the stored []int64 or the default.
// It normalises JSON float64 / json.Number values back to int64.
func (db *Database) GetInt64Slice(owner, key string, def []int64) []int64 {
	val := db.getValueCompat(owner, key, def)
	switch v := val.(type) {
	case []int64:
		res := make([]int64, len(v))
		copy(res, v)
		return res
	case []int:
		res := make([]int64, len(v))
		for i, item := range v {
			res[i] = int64(item)
		}
		return res
	case []any:
		res := make([]int64, 0, len(v))
		for _, item := range v {
			if id := asInt64(item, 0); id != 0 {
				res = append(res, id)
			}
		}
		return res
	}
	if def == nil {
		return []int64{}
	}
	res := make([]int64, len(def))
	copy(res, def)
	return res
}

// GetStringMap returns the stored map[string]string or the default.
func (db *Database) GetStringMap(owner, key string, def map[string]string) map[string]string {
	val := db.getValueCompat(owner, key, def)
	switch v := val.(type) {
	case map[string]string:
		res := make(map[string]string, len(v))
		for k, item := range v {
			res[k] = item
		}
		return res
	case map[string]any:
		res := make(map[string]string, len(v))
		for k, item := range v {
			res[k] = fmt.Sprintf("%v", item)
		}
		return res
	}
	if def == nil {
		return map[string]string{}
	}
	res := make(map[string]string, len(def))
	for k, item := range def {
		res[k] = item
	}
	return res
}

// SetStringSlice stores a []string value.
func (db *Database) SetStringSlice(owner, key string, value []string) error {
	return db.Set(owner, key, value)
}

// SetInt64Slice stores a []int64 value.
func (db *Database) SetInt64Slice(owner, key string, value []int64) error {
	return db.Set(owner, key, value)
}

// SetStringMap stores a map[string]string value.
func (db *Database) SetStringMap(owner, key string, value map[string]string) error {
	return db.Set(owner, key, value)
}

// SetString stores a string value.
func (db *Database) SetString(owner, key string, value string) error {
	return db.Set(owner, key, value)
}

// SetInt64 stores an int64 value.
func (db *Database) SetInt64(owner, key string, value int64) error {
	return db.Set(owner, key, value)
}

// SetBool stores a bool value.
func (db *Database) SetBool(owner, key string, value bool) error {
	return db.Set(owner, key, value)
}

// SetInt stores an int value.
func (db *Database) SetInt(owner, key string, value int) error {
	return db.Set(owner, key, value)
}

// SetAnyMap stores a map[string]any value.
func (db *Database) SetAnyMap(owner, key string, value map[string]any) error {
	return db.Set(owner, key, value)
}

func asInt64(v any, def int64) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		if id, err := strconv.ParseInt(x, 10, 64); err == nil {
			return id
		}
	case json.Number:
		if id, err := x.Int64(); err == nil {
			return id
		}
	}
	return def
}

// GetAnyMap returns the stored map[string]any or the default.
func (db *Database) GetAnyMap(owner, key string, def map[string]any) map[string]any {
	val := db.getValueCompat(owner, key, def)
	if m, ok := val.(map[string]any); ok {
		if res, ok := deepCopyValue(m).(map[string]any); ok && res != nil {
			return res
		}
	}
	if def == nil {
		return map[string]any{}
	}
	if res, ok := deepCopyValue(def).(map[string]any); ok {
		return res
	}
	return map[string]any{}
}

// GetStringMapStringSlice returns the stored map[string][]string or the default.
// It converts values produced by JSON unmarshalling.
func (db *Database) GetStringMapStringSlice(owner, key string, def map[string][]string) map[string][]string {
	val := db.getValueCompat(owner, key, def)
	switch v := val.(type) {
	case map[string][]string:
		res := make(map[string][]string, len(v))
		for k, item := range v {
			list := make([]string, len(item))
			copy(list, item)
			res[k] = list
		}
		return res
	case map[string]any:
		res := make(map[string][]string, len(v))
		for k, item := range v {
			var list []string
			if raw, err := json.Marshal(item); err == nil {
				_ = json.Unmarshal(raw, &list)
			}
			res[k] = list
		}
		return res
	}
	if def == nil {
		return map[string][]string{}
	}
	res := make(map[string][]string, len(def))
	for k, item := range def {
		list := make([]string, len(item))
		copy(list, item)
		res[k] = list
	}
	return res
}

// SetStringMapStringSlice stores a map[string][]string value.
func (db *Database) SetStringMapStringSlice(owner, key string, value map[string][]string) error {
	return db.Set(owner, key, value)
}

// GetStringMapInt returns the stored map[string]int or the default.
func (db *Database) GetStringMapInt(owner, key string, def map[string]int) map[string]int {
	val := db.getValueCompat(owner, key, def)
	switch v := val.(type) {
	case map[string]int:
		res := make(map[string]int, len(v))
		for k, item := range v {
			res[k] = item
		}
		return res
	case map[string]any:
		res := make(map[string]int, len(v))
		for k, item := range v {
			res[k] = int(asInt64(item, 0))
		}
		return res
	}
	if def == nil {
		return map[string]int{}
	}
	res := make(map[string]int, len(def))
	for k, item := range def {
		res[k] = item
	}
	return res
}

// SetStringMapInt stores a map[string]int value.
func (db *Database) SetStringMapInt(owner, key string, value map[string]int) error {
	return db.Set(owner, key, value)
}

func (db *Database) Dump() map[string]map[string]any {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.deepCopy(db.data)
}

func (db *Database) Set(owner, key string, value any) error {
	owner = db.normalizeOwner(owner)
	db.mu.RLock()
	if err := db.stateError("set", owner, key); err != nil {
		db.mu.RUnlock()
		return err
	}
	db.mu.RUnlock()
	// Stack trace check for write permissions
	if dbProtectedOwners[owner] {
		caller := db.getWriteCaller()
		if !dbAllowedWriters[caller] {
			L().Info("Blocked db write to protected owner={0} key={1} from {2}", zap.Any("arg0", owner), zap.Any("arg1", key), zap.Any("arg2", caller))
			return databaseError("set", owner, key, "", ErrDatabaseWriteProtected, nil)
		}
	}

	cloned, err := cloneJSONValue(value)
	if err != nil {
		L().Info("Attempted to write non-serializable object to db key={0}: {1}", zap.Any("arg0", key), zap.Any("arg1", err))
		return databaseError("set", owner, key, "local", ErrDatabaseInvalidValue, err)
	}

	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	db.mu.RLock()
	if err := db.stateError("set", owner, key); err != nil {
		db.mu.RUnlock()
		return err
	}
	candidate := db.deepCopy(db.data)
	db.mu.RUnlock()
	if candidate[owner] == nil {
		candidate[owner] = make(map[string]any)
	}
	candidate[owner][key] = cloned
	err = db.commitCandidate("set", owner, key, candidate, true)
	if err == nil {
		db.scheduleConfigReload(owner)
	}
	return err
}

func (db *Database) Delete(owner, key string) error {
	owner = db.normalizeOwner(owner)
	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	db.mu.RLock()
	if err := db.stateError("delete", owner, key); err != nil {
		db.mu.RUnlock()
		return err
	}
	candidate := db.deepCopy(db.data)
	db.mu.RUnlock()
	if mod, ok := candidate[owner]; ok {
		delete(mod, key)
	}
	err := db.commitCandidate("delete", owner, key, candidate, true)
	if err == nil {
		db.scheduleConfigReload(owner)
	}
	return err
}

// Reset clears the database and replaces all content with the given data.
func (db *Database) Reset(data map[string]map[string]any) error {
	db.mu.RLock()
	if err := db.stateError("reset", "", ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	db.mu.RUnlock()
	cloned, err := cloneJSONValue(data)
	if err != nil {
		L().Info("Attempted to reset db with non-serializable data: {0}", zap.Any("arg0", err))
		return databaseError("reset", "", "", "local", ErrDatabaseInvalidValue, err)
	}
	newData := cloned.(map[string]map[string]any)

	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	return db.commitCandidate("reset", "", "", newData, true)
}

func (db *Database) getWriteCaller() string {
	pc := make([]uintptr, 10)
	n := runtime.Callers(3, pc)
	frames := runtime.CallersFrames(pc[:n])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "Database") && !strings.Contains(frame.Function, "pointers") {
			return frame.Function
		}
		if !more {
			break
		}
	}
	return "unknown"
}

// PointerChecked returns a PointerList, PointerDict, or scalar value depending
// on the type of the current (or default) value stored under owner/key.
func (db *Database) PointerChecked(owner, key string, defaultValue any) (any, error) {
	owner = db.normalizeOwner(owner)
	value, err := db.Get(owner, key, defaultValue)
	if err != nil {
		return nil, err
	}
	switch value.(type) {
	case []any:
		var def []any
		if d, ok := defaultValue.([]any); ok {
			def = d
		}
		return NewPointerListChecked(db, owner, key, def)
	case map[string]any:
		var def map[string]any
		if d, ok := defaultValue.(map[string]any); ok {
			def = d
		}
		return NewPointerDictChecked(db, owner, key, def)
	default:
		return value, nil
	}
}

// Pointer preserves the original helper contract. On read failure it logs and
// returns a defensive copy of defaultValue; use PointerChecked when fallback is
// not safe.
func (db *Database) Pointer(owner, key string, defaultValue any) any {
	value, err := db.PointerChecked(owner, key, defaultValue)
	if err == nil {
		return value
	}
	L().Error("Database pointer load failed; using caller-provided fallback", zap.String("owner", owner), zap.String("key", key), zap.Error(err))
	return deepCopyValue(defaultValue)
}

// Update bulk-sets multiple owner/key/value entries, respecting write protection.
func (db *Database) Update(items map[string]map[string]any) error {
	db.mu.RLock()
	if err := db.stateError("update", "", ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	db.mu.RUnlock()
	cloned, err := cloneJSONValue(items)
	if err != nil {
		L().Info("Attempted to bulk write non-serializable data: {0}", zap.Any("arg0", err))
		return databaseError("update", "", "", "local", ErrDatabaseInvalidValue, err)
	}

	updates := make(map[string]map[string]any, len(items))
	for owner, keys := range cloned.(map[string]map[string]any) {
		normOwner := db.normalizeOwner(owner)
		if dbProtectedOwners[normOwner] {
			caller := db.getWriteCaller()
			if !dbAllowedWriters[caller] {
				L().Info("Blocked bulk db write to protected owner={0} from {1}", zap.Any("arg0", normOwner), zap.Any("arg1", caller))
				return databaseError("update", normOwner, "", "", ErrDatabaseWriteProtected, nil)
			}
		}
		if updates[normOwner] == nil {
			updates[normOwner] = make(map[string]any, len(keys))
		}
		for key, value := range keys {
			updates[normOwner][key] = value
		}
	}

	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	db.mu.RLock()
	if err := db.stateError("update", "", ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	candidate := db.deepCopy(db.data)
	db.mu.RUnlock()
	for owner, keys := range updates {
		if _, ok := candidate[owner]; !ok {
			candidate[owner] = make(map[string]any)
		}
		for k, v := range keys {
			candidate[owner][k] = v
		}
	}
	return db.commitCandidate("update", "", "", candidate, true)
}

// DeleteOwner removes all keys for an owner namespace from the database.
func (db *Database) DeleteOwner(owner string) error {
	owner = db.normalizeOwner(owner)
	db.persistMu.Lock()
	defer db.persistMu.Unlock()
	db.mu.RLock()
	if err := db.stateError("delete_owner", owner, ""); err != nil {
		db.mu.RUnlock()
		return err
	}
	candidate := db.deepCopy(db.data)
	db.mu.RUnlock()
	delete(candidate, owner)
	return db.commitCandidate("delete_owner", owner, "", candidate, true)
}

// GetAll returns a deep copy of the entire database for serialisation purposes.
func (db *Database) GetAll() map[string]map[string]any {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.deepCopy(db.data)
}

func (db *Database) deepCopy(src map[string]map[string]any) map[string]map[string]any {
	dst := make(map[string]map[string]any, len(src))
	for k, v := range src {
		inner, _ := deepCopyValue(v).(map[string]any)
		if inner == nil {
			inner = make(map[string]any)
		}
		dst[k] = inner
	}
	return dst
}

// ValueStore is the constraint for values that can be stored in the database.
// Only JSON-serializable primitives are allowed.
type ValueStore interface {
	~string | ~int | ~int64 | ~float64 | ~bool |
		[]string | []int | []int64 | []float64 | []bool |
		map[string]string | map[string]int | map[string]int64 | map[string]any
}

func deepCopyValue(src any) any {
	if src == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(src), make(map[cloneVisit]reflect.Value)).Interface()
}

func cloneJSONValue(src any) (any, error) {
	if _, err := json.Marshal(src); err != nil {
		return nil, err
	}
	if err := validateCloneableJSONValue(reflect.ValueOf(src)); err != nil {
		return nil, err
	}
	return deepCopyValue(src), nil
}

func validateCloneableJSONValue(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return validateCloneableJSONValue(value.Elem())
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := validateCloneableJSONValue(iter.Key()); err != nil {
				return err
			}
			if err := validateCloneableJSONValue(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := validateCloneableJSONValue(value.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() && typeContainsMutableAlias(field.Type, make(map[reflect.Type]bool)) {
				return fmt.Errorf("unexported mutable field %s.%s cannot be cloned safely", typ, field.Name)
			}
			if field.IsExported() {
				if err := validateCloneableJSONValue(value.Field(i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func typeContainsMutableAlias(typ reflect.Type, visiting map[reflect.Type]bool) bool {
	switch typ.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	case reflect.Array:
		return typeContainsMutableAlias(typ.Elem(), visiting)
	case reflect.Struct:
		if visiting[typ] {
			return false
		}
		visiting[typ] = true
		defer delete(visiting, typ)
		for i := 0; i < typ.NumField(); i++ {
			if typeContainsMutableAlias(typ.Field(i).Type, visiting) {
				return true
			}
		}
	}
	return false
}

type cloneVisit struct {
	typ  reflect.Type
	kind reflect.Kind
	ptr  uintptr
	len  int
	cap  int
}

func cloneReflectValue(src reflect.Value, seen map[cloneVisit]reflect.Value) reflect.Value {
	if !src.IsValid() {
		return src
	}

	switch src.Kind() {
	case reflect.Interface:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		cloned := cloneReflectValue(src.Elem(), seen)
		dst := reflect.New(src.Type()).Elem()
		dst.Set(cloned)
		return dst
	case reflect.Pointer:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		visit := cloneVisit{typ: src.Type(), kind: src.Kind(), ptr: src.Pointer()}
		if cloned, ok := seen[visit]; ok {
			return cloned
		}
		dst := reflect.New(src.Type().Elem())
		seen[visit] = dst
		dst.Elem().Set(cloneReflectValue(src.Elem(), seen))
		return dst
	case reflect.Map:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		visit := cloneVisit{typ: src.Type(), kind: src.Kind(), ptr: src.Pointer()}
		if cloned, ok := seen[visit]; ok {
			return cloned
		}
		dst := reflect.MakeMapWithSize(src.Type(), src.Len())
		seen[visit] = dst
		iter := src.MapRange()
		for iter.Next() {
			dst.SetMapIndex(cloneReflectValue(iter.Key(), seen), cloneReflectValue(iter.Value(), seen))
		}
		return dst
	case reflect.Slice:
		if src.IsNil() {
			return reflect.Zero(src.Type())
		}
		dst := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		if src.Pointer() != 0 {
			visit := cloneVisit{typ: src.Type(), kind: src.Kind(), ptr: src.Pointer(), len: src.Len(), cap: src.Cap()}
			if cloned, ok := seen[visit]; ok {
				return cloned
			}
			seen[visit] = dst
		}
		for i := 0; i < src.Len(); i++ {
			dst.Index(i).Set(cloneReflectValue(src.Index(i), seen))
		}
		return dst
	case reflect.Array:
		dst := reflect.New(src.Type()).Elem()
		for i := 0; i < src.Len(); i++ {
			dst.Index(i).Set(cloneReflectValue(src.Index(i), seen))
		}
		return dst
	case reflect.Struct:
		dst := reflect.New(src.Type()).Elem()
		dst.Set(src)
		for i := 0; i < src.NumField(); i++ {
			if dst.Field(i).CanSet() && src.Type().Field(i).IsExported() {
				dst.Field(i).Set(cloneReflectValue(src.Field(i), seen))
			}
		}
		return dst
	default:
		return src
	}
}

// StoreAsset stores a message or file to the assets channel.
// Thin façade over AssetRepository; does not hold DB locks during Telegram RPC.
func (db *Database) StoreAsset(message any) (int, error) {
	return db.assets.StoreAsset(message)
}

// FetchAsset fetches a previously saved asset by its asset_id.
// Thin façade over AssetRepository; does not hold DB locks during Telegram RPC.
func (db *Database) FetchAsset(assetID int) (*Message, error) {
	return db.assets.FetchAsset(assetID)
}
