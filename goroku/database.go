package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"goroku/goroku/utils"

	"github.com/gotd/td/tg"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var (
	dbProtectedOwners = map[string]bool{
		"GorokuPluginSecurity": true,
	}
	dbAllowedWriters = map[string]bool{
		"goroku/goroku/modules.(*GorokuPluginSecurity).UnexternalCmd": true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).ExternalCmd":   true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).AllowmodCmd":   true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).DenymodCmd":    true,
		"goroku/goroku/modules.(*GorokuPluginSecurity).TrustmodCmd":   true,
	}
)

type Database struct {
	mu          sync.RWMutex
	redisClient *redis.Client
	dbFile      string
	tgID        int64
	data        map[string]map[string]any
	revisions   []map[string]map[string]any
	nextRevCall int64
	client      *CustomTelegramClient
	// Redis batching: mirrors Python's asyncio.sleep(5) before redis save
	redisDirty    bool
	lastRedisSave int64
}

func NewDatabase(tgID int64) *Database {
	return &Database{
		tgID:      tgID,
		data:      make(map[string]map[string]any),
		revisions: make([]map[string]map[string]any, 0),
	}
}

func (db *Database) Init(redisURI string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if redisURI != "" {
		opt, err := redis.ParseURL(redisURI)
		if err == nil {
			db.redisClient = redis.NewClient(opt)
			go db.redisFlushLoop()
		}
	}

	db.dbFile = filepath.Join(BaseDir, fmt.Sprintf("config-%d.json", db.tgID))
	utils.SecureFile(db.dbFile)
	db.read()
	return nil
}

func (db *Database) redisFlushLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		db.mu.Lock()
		if db.redisClient == nil || !db.redisDirty {
			db.mu.Unlock()
			continue
		}
		bytes, err := json.Marshal(db.data)
		if err != nil {
			db.mu.Unlock()
			L().Info("Database Redis flush marshal failed: {0}", zap.Any("arg0", err))
			continue
		}
		db.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.redisClient.Set(ctx, fmt.Sprintf("%d", db.tgID), bytes, 0).Err()
		cancel()
		if err != nil {
			L().Info("Database Redis flush failed: {0}", zap.Any("arg0", err))
			continue
		}

		db.mu.Lock()
		db.lastRedisSave = time.Now().Unix()
		db.redisDirty = false
		db.mu.Unlock()
	}
}

func (db *Database) read() {
	if db.redisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		val, err := db.redisClient.Get(ctx, fmt.Sprintf("%d", db.tgID)).Result()
		if err == nil {
			var parsed map[string]map[string]any
			if err := json.Unmarshal([]byte(val), &parsed); err == nil {
				db.data = parsed
				return
			}
		}
		log.Println("Error reading Redis database, falling back to file")
	}

	// Read from local JSON file
	content, err := os.ReadFile(db.dbFile)
	if err != nil {
		if !os.IsNotExist(err) {
			L().Info("Error reading database file: {0}", zap.Any("arg0", err))
		}
		db.data = make(map[string]map[string]any)
		return
	}

	var parsed map[string]map[string]any
	// Convert legacy names if present in the string (similar to python's regex replacement)
	dbStr := string(content)
	dbStr = strings.ReplaceAll(dbStr, "hikka.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "legacy.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "heroku.", "goroku.")

	if err := json.Unmarshal([]byte(dbStr), &parsed); err == nil {
		db.data = parsed
	} else {
		L().Info("Database read failed! Creating new one... Error: {0}", zap.Any("arg0", err))
		db.data = make(map[string]map[string]any)
	}
}

func (db *Database) Save() bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.saveInner()
}

// saveInner performs the actual persistence. Must be called with db.mu held.
func (db *Database) saveInner() bool {
	// Perform database auto-fix
	db.processDBAutofix()

	now := time.Now().Unix()
	if db.nextRevCall < now {
		// Deep copy for revisions
		cloned := db.deepCopy(db.data)
		db.revisions = append(db.revisions, cloned)
		db.nextRevCall = now + 3
	}

	// Cap revisions at 15
	if len(db.revisions) > 15 {
		db.revisions = db.revisions[len(db.revisions)-15:]
	}

	// Redis batching: mirror Python's asyncio.sleep(5) before writing to Redis.
	// Only flush to Redis if >=5 seconds have passed since the last Redis write.
	if db.redisClient != nil {
		if now-db.lastRedisSave >= 5 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			bytes, err := json.Marshal(db.data)
			if err == nil {
				err = db.redisClient.Set(ctx, fmt.Sprintf("%d", db.tgID), bytes, 0).Err()
				if err == nil {
					db.lastRedisSave = now
					db.redisDirty = false
					return true
				}
			}
		} else {
			// Mark dirty — will be picked up in the next flush cycle
			db.redisDirty = true
			// Still fall through to write the local file as immediate backup
		}
	}

	bytes, err := json.MarshalIndent(db.data, "", "    ")
	if err != nil {
		L().Info("Database save failed to marshal: {0}", zap.Any("arg0", err))
		return false
	}

	err = os.WriteFile(db.dbFile, bytes, 0600)
	utils.SecureFile(db.dbFile)
	if err != nil {
		L().Info("Database save failed: {0}", zap.Any("arg0", err))
		return false
	}

	return true
}

// Rollback restores the database from the latest saved revision.
// Returns true if a revision was available and successfully restored.
func (db *Database) Rollback() bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	if len(db.revisions) == 0 {
		log.Println("Database rollback: no revisions available")
		return false
	}

	// Pop the latest revision
	rev := db.revisions[len(db.revisions)-1]
	db.revisions = db.revisions[:len(db.revisions)-1]

	db.data = rev
	log.Println("Database rollback: restored from revision")
	return db.saveInner()
}

func (db *Database) processDBAutofix() {
	for modName, keys := range db.data {
		if keys == nil {
			delete(db.data, modName)
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
	if db.client != nil && db.client.Loader != nil {
		for _, mod := range db.client.Loader.GetModules() {
			if strings.EqualFold(mod.Name(), owner) {
				return mod.Name()
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

	if mod, ok := db.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return val, nil
		}
	}
	return defaultValue, nil
}

// GetValue returns the stored value or default without an error.
// Deprecated: use Get() when error handling matters.
func (db *Database) GetValue(owner, key string, defaultValue any) any {
	val, _ := db.Get(owner, key, defaultValue)
	return val
}

// GetString returns the stored string value or the default.
func (db *Database) GetString(owner, key, def string) string {
	val, _ := db.Get(owner, key, def)
	if s, ok := val.(string); ok {
		return s
	}
	return def
}

// GetInt64 returns the stored integer value or the default.
// It normalises JSON float64 / json.Number values back to int64.
func (db *Database) GetInt64(owner, key string, def int64) int64 {
	val, _ := db.Get(owner, key, def)
	return asInt64(val, def)
}

// GetInt returns the stored int or the default.
func (db *Database) GetInt(owner, key string, def int) int {
	val, _ := db.Get(owner, key, def)
	return int(asInt64(val, int64(def)))
}

// GetBool returns the stored boolean value or the default.
func (db *Database) GetBool(owner, key string, def bool) bool {
	val, _ := db.Get(owner, key, def)
	if b, ok := val.(bool); ok {
		return b
	}
	return def
}

// GetStringSlice returns the stored []string or the default.
// It converts []any produced by JSON unmarshalling.
func (db *Database) GetStringSlice(owner, key string, def []string) []string {
	val, _ := db.Get(owner, key, def)
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
	val, _ := db.Get(owner, key, def)
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
	val, _ := db.Get(owner, key, def)
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
func (db *Database) SetStringSlice(owner, key string, value []string) bool {
	return db.Set(owner, key, value)
}

// SetInt64Slice stores a []int64 value.
func (db *Database) SetInt64Slice(owner, key string, value []int64) bool {
	return db.Set(owner, key, value)
}

// SetStringMap stores a map[string]string value.
func (db *Database) SetStringMap(owner, key string, value map[string]string) bool {
	return db.Set(owner, key, value)
}

// SetString stores a string value.
func (db *Database) SetString(owner, key string, value string) bool {
	return db.Set(owner, key, value)
}

// SetInt64 stores an int64 value.
func (db *Database) SetInt64(owner, key string, value int64) bool {
	return db.Set(owner, key, value)
}

// SetBool stores a bool value.
func (db *Database) SetBool(owner, key string, value bool) bool {
	return db.Set(owner, key, value)
}

// SetInt stores an int value.
func (db *Database) SetInt(owner, key string, value int) bool {
	return db.Set(owner, key, value)
}

// SetAnyMap stores a map[string]any value.
func (db *Database) SetAnyMap(owner, key string, value map[string]any) bool {
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
	val, _ := db.Get(owner, key, def)
	if m, ok := val.(map[string]any); ok {
		if res, ok := deepCopyValue(m).(map[string]any); ok {
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
	val, _ := db.Get(owner, key, def)
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
func (db *Database) SetStringMapStringSlice(owner, key string, value map[string][]string) bool {
	return db.Set(owner, key, value)
}

// GetStringMapInt returns the stored map[string]int or the default.
func (db *Database) GetStringMapInt(owner, key string, def map[string]int) map[string]int {
	val, _ := db.Get(owner, key, def)
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
func (db *Database) SetStringMapInt(owner, key string, value map[string]int) bool {
	return db.Set(owner, key, value)
}

func (db *Database) Dump() map[string]map[string]any {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.deepCopy(db.data)
}

func (db *Database) Set(owner, key string, value any) bool {
	owner = db.normalizeOwner(owner)
	// Stack trace check for write permissions
	if dbProtectedOwners[owner] {
		caller := db.getWriteCaller()
		if !dbAllowedWriters[caller] {
			L().Info("Blocked db write to protected owner={0} key={1} from {2}", zap.Any("arg0", owner), zap.Any("arg1", key), zap.Any("arg2", caller))
			return false
		}
	}

	// Validate JSON serializability
	_, err := json.Marshal(value)
	if err != nil {
		L().Info("Attempted to write non-serializable object to db key={0}: {1}", zap.Any("arg0", key), zap.Any("arg1", err))
		return false
	}

	db.mu.Lock()
	if _, ok := db.data[owner]; !ok {
		db.data[owner] = make(map[string]any)
	}
	db.data[owner][key] = value
	db.mu.Unlock()

	saved := db.Save()
	if saved && db.client != nil && db.client.Loader != nil {
		go db.client.Loader.ReloadModuleConfig(owner)
	}
	return saved
}

func (db *Database) Delete(owner, key string) bool {
	owner = db.normalizeOwner(owner)
	db.mu.Lock()
	if mod, ok := db.data[owner]; ok {
		delete(mod, key)
	}
	db.mu.Unlock()

	saved := db.Save()
	if saved && db.client != nil && db.client.Loader != nil {
		go db.client.Loader.ReloadModuleConfig(owner)
	}
	return saved
}

// Reset clears the database and replaces all content with the given data.
func (db *Database) Reset(data map[string]map[string]any) bool {
	db.mu.Lock()
	db.data = data
	db.mu.Unlock()
	return db.Save()
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

// Pointer returns a PointerList, PointerDict, or scalar value depending on the
// type of the current (or default) value stored under owner/key.
// This mirrors Python's Database.pointer() helper.
func (db *Database) Pointer(owner, key string, defaultValue any) any {
	owner = db.normalizeOwner(owner)
	value, _ := db.Get(owner, key, defaultValue)
	switch value.(type) {
	case []any:
		var def []any
		if d, ok := defaultValue.([]any); ok {
			def = d
		}
		return NewPointerList(db, owner, key, def)
	case map[string]any:
		var def map[string]any
		if d, ok := defaultValue.(map[string]any); ok {
			def = d
		}
		return NewPointerDict(db, owner, key, def)
	default:
		return value
	}
}

// Update bulk-sets multiple owner/key/value entries, respecting write protection.
func (db *Database) Update(items map[string]map[string]any) bool {
	for owner, keys := range items {
		normOwner := db.normalizeOwner(owner)
		if dbProtectedOwners[normOwner] {
			caller := db.getWriteCaller()
			if !dbAllowedWriters[caller] {
				L().Info("Blocked bulk db write to protected owner={0} from {1}", zap.Any("arg0", normOwner), zap.Any("arg1", caller))
				return false
			}
		}
		db.mu.Lock()
		if _, ok := db.data[normOwner]; !ok {
			db.data[normOwner] = make(map[string]any)
		}
		for k, v := range keys {
			db.data[normOwner][k] = v
		}
		db.mu.Unlock()
	}
	return db.Save()
}

// DeleteOwner removes all keys for an owner namespace from the database.
func (db *Database) DeleteOwner(owner string) bool {
	owner = db.normalizeOwner(owner)
	db.mu.Lock()
	delete(db.data, owner)
	db.mu.Unlock()
	return db.Save()
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
	switch v := src.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for key, value := range v {
			m[key] = deepCopyValue(value)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, value := range v {
			s[i] = deepCopyValue(value)
		}
		return s
	case []string:
		return append([]string(nil), v...)
	case []int:
		return append([]int(nil), v...)
	case []int64:
		return append([]int64(nil), v...)
	case []float64:
		return append([]float64(nil), v...)
	case []bool:
		return append([]bool(nil), v...)
	default:
		return v
	}
}

// StoreAsset stores a message or file to the assets channel.
// Returns the message ID (asset ID).
func (db *Database) StoreAsset(message any) (int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.client == nil {
		return 0, fmt.Errorf("client not initialized in database")
	}

	forumsCache := db.GetAnyMap("goroku.forums", "forums_cache", nil)
	var assetsTopicID int
	if hubot, ok := forumsCache["goroku-userbot"].(map[string]any); ok {
		if assetsVal, ok := hubot["Assets"]; ok {
			switch v := assetsVal.(type) {
			case float64:
				assetsTopicID = int(v)
			case int64:
				assetsTopicID = int(v)
			case int:
				assetsTopicID = v
			}
		}
	}
	if assetsTopicID == 0 {
		return 0, fmt.Errorf("Tried to save asset to non-existing asset topic.")
	}

	contentChannelVal := db.GetInt64("goroku.forums", "channel_id", 0)
	if contentChannelVal == 0 {
		return 0, fmt.Errorf("Tried to save asset with non-existing content channel.")
	}

	targetChatID := int64(-1000000000000 - contentChannelVal)

	opts := []MsgOption{WithReplyTo(int64(assetsTopicID))}

	var msgID int64

	switch msgVal := message.(type) {
	case *Message:
		res, err := db.client.SendMessageWithOptions(ChatRefID(targetChatID), msgVal.Text, opts...)
		if err != nil {
			return 0, err
		}
		msgID = GetSentMessageID(res)
	case string:
		if _, statErr := os.Stat(msgVal); statErr == nil {
			res, err := db.client.SendFileWithOptions(ChatRefID(targetChatID), msgVal, "", opts...)
			if err != nil {
				return 0, err
			}
			msgID = GetSentMessageID(res)
		} else {
			res, err := db.client.SendMessageWithOptions(ChatRefID(targetChatID), msgVal, opts...)
			if err != nil {
				return 0, err
			}
			msgID = GetSentMessageID(res)
		}
	default:
		res, err := db.client.SendFileWithOptions(ChatRefID(targetChatID), msgVal, "", opts...)
		if err != nil {
			return 0, err
		}
		msgID = GetSentMessageID(res)
	}

	return int(msgID), nil
}

// FetchAsset Fetch previously saved asset by its asset_id
func (db *Database) FetchAsset(assetID int) (*Message, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.client == nil {
		return nil, fmt.Errorf("client not initialized in database")
	}

	forumsCache := db.GetAnyMap("goroku.forums", "forums_cache", nil)
	var assetsTopicID int
	if hubot, ok := forumsCache["goroku-userbot"].(map[string]any); ok {
		if assetsVal, ok := hubot["Assets"]; ok {
			switch v := assetsVal.(type) {
			case float64:
				assetsTopicID = int(v)
			case int64:
				assetsTopicID = int(v)
			case int:
				assetsTopicID = v
			}
		}
	}
	if assetsTopicID == 0 {
		return nil, fmt.Errorf("Tried to fetch asset from non-existing asset topic.")
	}

	contentChannelVal := db.GetInt64("goroku.forums", "channel_id", 0)
	if contentChannelVal == 0 {
		return nil, fmt.Errorf("Tried to fetch asset with non-existing content channel.")
	}

	peer, err := db.client.ResolvePeer(contentChannelVal)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve content channel: %v", err)
	}

	var inputChannel tg.InputChannelClass
	if peerChan, ok := peer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{
			ChannelID:  peerChan.ChannelID,
			AccessHash: peerChan.AccessHash,
		}
	} else {
		return nil, fmt.Errorf("content channel is not a channel peer")
	}

	res, err := db.client.rawAPI.ChannelsGetMessages(db.client.ctx, &tg.ChannelsGetMessagesRequest{
		Channel: inputChannel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: assetID}},
	})
	if err != nil {
		return nil, err
	}

	var msg *tg.Message
	switch mClass := res.(type) {
	case *tg.MessagesMessagesSlice:
		if len(mClass.Messages) > 0 {
			if tgMsg, ok := mClass.Messages[0].(*tg.Message); ok {
				msg = tgMsg
			}
		}
	case *tg.MessagesMessages:
		if len(mClass.Messages) > 0 {
			if tgMsg, ok := mClass.Messages[0].(*tg.Message); ok {
				msg = tgMsg
			}
		}
	case *tg.MessagesChannelMessages:
		if len(mClass.Messages) > 0 {
			if tgMsg, ok := mClass.Messages[0].(*tg.Message); ok {
				msg = tgMsg
			}
		}
	}

	if msg == nil {
		return nil, ErrNotFound
	}

	hMsg := &Message{
		ID:      int64(msg.ID),
		Text:    entitiesToHTML(msg.Message, msg.Entities),
		RawText: msg.Message,
		Out:     msg.Out,
		Client:  db.client,
	}
	return hMsg, nil
}
