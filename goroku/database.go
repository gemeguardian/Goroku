package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	"goroku/goroku/utils"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/redis/go-redis/v9"
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
	data        map[string]map[string]interface{}
	revisions   []map[string]map[string]interface{}
	nextRevCall int64
	client      *CustomTelegramClient
	// Redis batching: mirrors Python's asyncio.sleep(5) before redis save
	redisDirty    bool
	lastRedisSave int64
}

func NewDatabase(tgID int64) *Database {
	return &Database{
		tgID:      tgID,
		data:      make(map[string]map[string]interface{}),
		revisions: make([]map[string]map[string]interface{}, 0),
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
			log.Printf("Database Redis flush marshal failed: %v\n", err)
			continue
		}
		db.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.redisClient.Set(ctx, fmt.Sprintf("%d", db.tgID), bytes, 0).Err()
		cancel()
		if err != nil {
			log.Printf("Database Redis flush failed: %v\n", err)
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
			var parsed map[string]map[string]interface{}
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
			log.Printf("Error reading database file: %v\n", err)
		}
		db.data = make(map[string]map[string]interface{})
		return
	}

	var parsed map[string]map[string]interface{}
	// Convert legacy names if present in the string (similar to python's regex replacement)
	dbStr := string(content)
	dbStr = strings.ReplaceAll(dbStr, "hikka.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "legacy.", "goroku.")
	dbStr = strings.ReplaceAll(dbStr, "heroku.", "goroku.")

	if err := json.Unmarshal([]byte(dbStr), &parsed); err == nil {
		db.data = parsed
	} else {
		log.Printf("Database read failed! Creating new one... Error: %v\n", err)
		db.data = make(map[string]map[string]interface{})
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
		log.Printf("Database save failed to marshal: %v\n", err)
		return false
	}

	err = os.WriteFile(db.dbFile, bytes, 0600)
	utils.SecureFile(db.dbFile)
	if err != nil {
		log.Printf("Database save failed: %v\n", err)
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
		if loader, ok := db.client.Loader.(*Modules); ok && loader != nil {
			for _, mod := range loader.GetModules() {
				if strings.EqualFold(mod.Name(), owner) {
					return mod.Name()
				}
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

func (db *Database) Get(owner, key string, defaultValue interface{}) interface{} {
	owner = db.normalizeOwner(owner)
	db.mu.RLock()
	defer db.mu.RUnlock()

	if mod, ok := db.data[owner]; ok {
		if val, ok := mod[key]; ok {
			return val
		}
	}
	return defaultValue
}

func (db *Database) Dump() map[string]map[string]interface{} {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.deepCopy(db.data)
}

func (db *Database) Set(owner, key string, value interface{}) bool {
	owner = db.normalizeOwner(owner)
	// Stack trace check for write permissions
	if dbProtectedOwners[owner] {
		caller := db.getWriteCaller()
		if !dbAllowedWriters[caller] {
			log.Printf("Blocked db write to protected owner=%s key=%s from %s\n", owner, key, caller)
			return false
		}
	}

	// Validate JSON serializability
	_, err := json.Marshal(value)
	if err != nil {
		log.Printf("Attempted to write non-serializable object to db key=%s: %v\n", key, err)
		return false
	}

	db.mu.Lock()
	if _, ok := db.data[owner]; !ok {
		db.data[owner] = make(map[string]interface{})
	}
	db.data[owner][key] = value
	db.mu.Unlock()

	saved := db.Save()
	if saved && db.client != nil && db.client.Loader != nil {
		type configReloader interface {
			ReloadModuleConfig(name string)
		}
		if reloader, ok := db.client.Loader.(configReloader); ok {
			go reloader.ReloadModuleConfig(owner)
		}
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
		type configReloader interface {
			ReloadModuleConfig(name string)
		}
		if reloader, ok := db.client.Loader.(configReloader); ok {
			go reloader.ReloadModuleConfig(owner)
		}
	}
	return saved
}

// Reset clears the database and replaces all content with the given data.
func (db *Database) Reset(data map[string]map[string]interface{}) bool {
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
func (db *Database) Pointer(owner, key string, defaultValue interface{}) interface{} {
	owner = db.normalizeOwner(owner)
	value := db.Get(owner, key, defaultValue)
	switch value.(type) {
	case []interface{}:
		var def []interface{}
		if d, ok := defaultValue.([]interface{}); ok {
			def = d
		}
		return NewPointerList(db, owner, key, def)
	case map[string]interface{}:
		var def map[string]interface{}
		if d, ok := defaultValue.(map[string]interface{}); ok {
			def = d
		}
		return NewPointerDict(db, owner, key, def)
	default:
		return value
	}
}

// Update bulk-sets multiple owner/key/value entries, respecting write protection.
func (db *Database) Update(items map[string]map[string]interface{}) bool {
	for owner, keys := range items {
		normOwner := db.normalizeOwner(owner)
		if dbProtectedOwners[normOwner] {
			caller := db.getWriteCaller()
			if !dbAllowedWriters[caller] {
				log.Printf("Blocked bulk db write to protected owner=%s from %s\n", normOwner, caller)
				return false
			}
		}
		db.mu.Lock()
		if _, ok := db.data[normOwner]; !ok {
			db.data[normOwner] = make(map[string]interface{})
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
func (db *Database) GetAll() map[string]map[string]interface{} {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.deepCopy(db.data)
}

func (db *Database) deepCopy(src map[string]map[string]interface{}) map[string]map[string]interface{} {
	dst := make(map[string]map[string]interface{}, len(src))
	for k, v := range src {
		inner, _ := deepCopyValue(v).(map[string]interface{})
		if inner == nil {
			inner = make(map[string]interface{})
		}
		dst[k] = inner
	}
	return dst
}

func deepCopyValue(src interface{}) interface{} {
	switch v := src.(type) {
	case map[string]interface{}:
		m := make(map[string]interface{}, len(v))
		for key, value := range v {
			m[key] = deepCopyValue(value)
		}
		return m
	case []interface{}:
		s := make([]interface{}, len(v))
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
func (db *Database) StoreAsset(message interface{}) (int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.client == nil {
		return 0, fmt.Errorf("client not initialized in database")
	}

	forumsCacheVal := db.Get("goroku.forums", "forums_cache", map[string]interface{}{})
	var assetsTopicID int
	if forumsCache, ok := forumsCacheVal.(map[string]interface{}); ok {
		if hubot, ok := forumsCache["goroku-userbot"].(map[string]interface{}); ok {
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
	}
	if assetsTopicID == 0 {
		return 0, fmt.Errorf("Tried to save asset to non-existing asset topic.")
	}

	contentChannelVal := db.Get("goroku.forums", "channel_id", nil)
	if contentChannelVal == nil {
		return 0, fmt.Errorf("Tried to save asset with non-existing content channel.")
	}

	peer, err := db.client.ResolvePeer(contentChannelVal)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve content channel: %v", err)
	}

	replyTo := &tg.InputReplyToMessage{
		ReplyToMsgID: assetsTopicID,
	}
	replyTo.SetTopMsgID(assetsTopicID)

	var msgID int

	switch msgVal := message.(type) {
	case *Message:
		// Send as text message
		res, err := db.client.rawAPI.MessagesSendMessage(db.client.ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  msgVal.Text,
			ReplyTo:  replyTo,
			RandomID: rand.Int63(),
		})
		if err != nil {
			return 0, err
		}
		if updates, ok := res.(*tg.Updates); ok {
			for _, u := range updates.Updates {
				if update, ok := u.(*tg.UpdateNewMessage); ok {
					if m, ok := update.Message.(*tg.Message); ok {
						msgID = m.ID
					}
				}
			}
		}
	case string:
		// Check if it's a file path or just text
		if _, statErr := os.Stat(msgVal); statErr == nil {
			// Send as file
			res, err := db.uploadAndSendFile(peer, msgVal, replyTo)
			if err != nil {
				return 0, err
			}
			msgID = res
		} else {
			// Send as text
			res, err := db.client.rawAPI.MessagesSendMessage(db.client.ctx, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  msgVal,
				ReplyTo:  replyTo,
				RandomID: rand.Int63(),
			})
			if err != nil {
				return 0, err
			}
			if updates, ok := res.(*tg.Updates); ok {
				for _, u := range updates.Updates {
					if update, ok := u.(*tg.UpdateNewMessage); ok {
						if m, ok := update.Message.(*tg.Message); ok {
							msgID = m.ID
						}
					}
				}
			}
		}
	default:
		// Try to send as uploaded file/bytes/reader
		res, err := db.uploadAndSendFile(peer, msgVal, replyTo)
		if err != nil {
			return 0, err
		}
		msgID = res
	}

	return msgID, nil
}

func (db *Database) uploadAndSendFile(peer tg.InputPeerClass, file interface{}, replyTo tg.InputReplyToClass) (int, error) {
	up := uploader.NewUploader(db.client.rawAPI)
	var inputFile tg.InputFileClass
	var filename string
	var mimeType string = "application/octet-stream"
	var err error

	switch v := file.(type) {
	case string:
		filename = filepath.Base(v)
		inputFile, err = up.FromPath(db.client.ctx, v)
		if err != nil {
			return 0, err
		}
	case []byte:
		filename = "file.bin"
		inputFile, err = up.FromBytes(db.client.ctx, filename, v)
		if err != nil {
			return 0, err
		}
	case io.Reader:
		filename = "file.bin"
		inputFile, err = up.FromReader(db.client.ctx, filename, v)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unsupported file type: %T", file)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	}

	var media tg.InputMediaClass
	if mimeType == "image/jpeg" || mimeType == "image/png" {
		photoMedia := &tg.InputMediaUploadedPhoto{
			File: inputFile,
		}
		photoMedia.SetFlags()
		media = photoMedia
	} else {
		media = &tg.InputMediaUploadedDocument{
			File:     inputFile,
			MimeType: mimeType,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: filename},
			},
		}
	}

	res, err := db.client.rawAPI.MessagesSendMedia(db.client.ctx, &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		ReplyTo:  replyTo,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return 0, err
	}

	if updates, ok := res.(*tg.Updates); ok {
		for _, u := range updates.Updates {
			if update, ok := u.(*tg.UpdateNewMessage); ok {
				if m, ok := update.Message.(*tg.Message); ok {
					return m.ID, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("could not extract message ID from updates")
}

// FetchAsset Fetch previously saved asset by its asset_id
func (db *Database) FetchAsset(assetID int) (*Message, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.client == nil {
		return nil, fmt.Errorf("client not initialized in database")
	}

	forumsCacheVal := db.Get("goroku.forums", "forums_cache", map[string]interface{}{})
	var assetsTopicID int
	if forumsCache, ok := forumsCacheVal.(map[string]interface{}); ok {
		if hubot, ok := forumsCache["goroku-userbot"].(map[string]interface{}); ok {
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
	}
	if assetsTopicID == 0 {
		return nil, fmt.Errorf("Tried to fetch asset from non-existing asset topic.")
	}

	contentChannelVal := db.Get("goroku.forums", "channel_id", nil)
	if contentChannelVal == nil {
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
		return nil, nil
	}

	hMsg := &Message{
		ID:      int64(msg.ID),
		Text:    msg.Message,
		RawText: msg.Message,
		Out:     msg.Out,
		Client:  db.client,
	}
	return hMsg, nil
}
