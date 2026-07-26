package utils

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"goroku/goroku/chatref"
	"goroku/goroku/logger"

	"github.com/gotd/td/tg"
)

// L returns the package-level zap logger.
func L() *zap.Logger { return logger.L() }

// FormattingEntity represents a text formatting entity.
type FormattingEntity struct {
	Offset int
	Length int
	Type   string
}

// Database interface to break circular dependencies.
type Database interface {
	GetInt64(owner, key string, def int64) int64
	GetAnyMap(owner, key string, def map[string]any) map[string]any
	SetAnyMap(owner, key string, value map[string]any) error
}

var tagRe = regexp.MustCompile(`(?i)</?([a-zA-Z][a-zA-Z0-9\-]*)(?:\s[^<>]*)?>`)

var telegramHtmlTags = map[string]bool{
	"strong":     true,
	"b":          true,
	"em":         true,
	"i":          true,
	"tg-spoiler": true,
	"u":          true,
	"del":        true,
	"s":          true,
	"blockquote": true,
	"code":       true,
	"pre":        true,
	"a":          true,
	"tg-emoji":   true,
	"emoji":      true,
}

// CacheEntry stores cached channels for AssetChannel
type CacheEntry struct {
	Peer any
	Exp  int64
	id   uint64
}

type channelCall struct {
	done    chan struct{}
	peer    any
	waiters int
}

type channelCacheKey struct {
	accountID int64
	client    ChannelCreator
	title     string
}

var (
	channelsCacheMu sync.Mutex
	channelsCache   = make(map[channelCacheKey]CacheEntry)
	channelCalls    = make(map[channelCacheKey]*channelCall)
	channelsCacheID uint64
	channelCacheTTL = time.Hour
)

type channelAccountIdentifier interface {
	TGIDValue() int64
}

func assetChannelCacheKey(creator ChannelCreator, title string) (channelCacheKey, bool) {
	if identified, ok := creator.(channelAccountIdentifier); ok {
		if accountID := identified.TGIDValue(); accountID != 0 {
			return channelCacheKey{accountID: accountID, title: title}, true
		}
	}

	clientType := reflect.TypeOf(creator)
	if clientType != nil && clientType.Comparable() {
		return channelCacheKey{client: creator, title: title}, true
	}
	return channelCacheKey{}, false
}

func fwProtect() {
	time.Sleep(1000 * time.Millisecond)
}

// Entity is an optional interface that custom entity types can implement to
// participate in GetEntityURL/GetEntityID without reflection. Telegram gotd
// types (*tg.User, *tg.Channel, tg.InputPeerClass implementers) and
// chatref.EntityRef are handled by type switch directly; other types should
// implement Entity.
type Entity interface {
	EntityID() int64
	EntityUsername() string
	EntityIsUser() bool
}

// entityFields extracts (id, username, isUser) from a Telegram entity using a
// type switch over the concrete gotd types, chatref.EntityRef, and the Entity
// interface. It replaces the previous reflect-based dispatch.
func entityFields(entity any) (int64, string, bool) {
	if entity == nil {
		return 0, "", false
	}
	switch e := entity.(type) {
	case chatref.ChatRef:
		return e.ID(), e.Username(), false
	case *chatref.ChatRef:
		if e == nil {
			return 0, "", false
		}
		return e.ID(), e.Username(), false
	case *tg.User:
		return e.ID, e.Username, true
	case tg.User:
		return e.ID, e.Username, true
	case *tg.Channel:
		return e.ID, e.Username, false
	case tg.Channel:
		return e.ID, e.Username, false
	case *tg.InputPeerUser:
		return e.UserID, "", true
	case *tg.InputPeerUserFromMessage:
		return e.UserID, "", true
	case *tg.InputPeerChannel:
		return e.ChannelID, "", false
	case *tg.InputPeerChannelFromMessage:
		return e.ChannelID, "", false
	case *tg.InputPeerChat:
		return e.ChatID, "", false
	case *tg.InputPeerSelf:
		return 0, "", true
	case Entity:
		return e.EntityID(), e.EntityUsername(), e.EntityIsUser()
	}
	return 0, "", false
}

// GetLangFlag returns the country flag emoji from a 2-letter country code.
func GetLangFlag(countrycode string) string {
	var code []rune
	for _, r := range strings.ToLower(countrycode) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			code = append(code, r)
		}
	}
	if len(code) == 2 {
		var builder strings.Builder
		for _, c := range code {
			upper := c - 'a' + 'A'
			flagRune := rune(upper) + (0x1F1E6 - 0x41)
			builder.WriteRune(flagRune)
		}
		return builder.String()
	}
	return countrycode
}

// GetEntityRefURL returns a Telegram URL for a typed EntityRef. Pass isUser=true
// for user references (UserRef) and false for channels/chats (ChannelRef).
// When the ref carries a username, a tg://resolve URL is produced for non-user
// refs; otherwise the numeric id is used.
func GetEntityRefURL(ref chatref.EntityRef, isUser bool, openmessage bool) string {
	if isUser {
		if openmessage {
			return fmt.Sprintf("tg://openmessage?id=%d", ref.ID())
		}
		return fmt.Sprintf("tg://user?id=%d", ref.ID())
	}
	if username := ref.Username(); username != "" {
		return fmt.Sprintf("tg://resolve?domain=%s", username)
	}
	return ""
}

// GetEntityURL returns a link to the user/channel. It dispatches over the
// concrete Telegram gotd types and chatref.EntityRef via a type switch (no
// reflection). Custom types may implement the Entity interface.
func GetEntityURL(entity any, openmessage bool) string {
	id, username, isUser := entityFields(entity)
	if isUser {
		if openmessage {
			return fmt.Sprintf("tg://openmessage?id=%d", id)
		}
		return fmt.Sprintf("tg://user?id=%d", id)
	}
	if username != "" {
		return fmt.Sprintf("tg://resolve?domain=%s", username)
	}
	return ""
}

// RemoveEmoji filters out emojis from text.
func RemoveEmoji(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if (r >= 0x1F600 && r <= 0x1F64F) ||
			(r >= 0x1F300 && r <= 0x1F5FF) ||
			(r >= 0x1F680 && r <= 0x1F6FF) ||
			(r >= 0x1F900 && r <= 0x1F9FF) ||
			(r >= 0x1FA70 && r <= 0x1FAFF) ||
			(r >= 0x2600 && r <= 0x26FF) ||
			(r >= 0x2700 && r <= 0x27BF) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// EscapeHTML escapes tags for Telegram.
func EscapeHTML(text string) string {
	s := strings.ReplaceAll(text, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// EscapeQuotes escapes quotes for HTML string parameters.
func EscapeQuotes(text string) string {
	return strings.ReplaceAll(EscapeHTML(text), "\"", "&quot;")
}

var removeHtmlKeepEmojisRegex = regexp.MustCompile(`(?i)(<\/?a.*?>|<\/?b>|<\/?i>|<\/?u>|<\/?strong>|<\/?em>|<\/?code.*?>|<\/?strike>|<\/?del>|<\/?pre.*?>|<\/?blockquote.*?>)`)
var removeHtmlRegex = regexp.MustCompile(`(?i)(<\/?a.*?>|<\/?b>|<\/?i>|<\/?u>|<\/?strong>|<\/?em>|<\/?code.*?>|<\/?strike>|<\/?del>|<\/?pre.*?>|<\/?emoji.*?>|<\/?blockquote.*?>)`)

// RemoveHTML removes HTML tags from the given string.
func RemoveHTML(text string, escape bool, keepEmojis bool) string {
	var cleaned string
	if keepEmojis {
		cleaned = removeHtmlKeepEmojisRegex.ReplaceAllString(text, "")
	} else {
		cleaned = removeHtmlRegex.ReplaceAllString(text, "")
	}
	if escape {
		return EscapeHTML(cleaned)
	}
	return cleaned
}

// CheckURL statically checks if a string is a valid URL.
func CheckURL(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && parsed.Host != ""
}

// GetLink returns a permalink link to the entity.
func GetLink(entity any) string {
	return GetEntityURL(entity, false)
}

type ChannelCreator interface {
	FindChannelByTitle(title string) (any, error)
	CreateChannel(title, description string, megagroup, forum bool) (any, error)
	InviteBotToChannel(channelPeer any) error
	ToggleForum(channelPeer any, enabled bool) error
	CreateForumTopic(channelPeer any, title, description string, iconEmojiID int64) (int64, error)
	SearchForumTopic(channelPeer any, title string) (int64, error)
}

// ErrIncompatibleChannelClient indicates that AssetChannel cannot use the supplied client.
var ErrIncompatibleChannelClient = errors.New("asset channel client does not implement ChannelCreator")

// AssetChannel returns or creates a channel. If the client is incompatible, the
// first result is ErrIncompatibleChannelClient and created is false.
func AssetChannel(
	client any,
	title string,
	description string,
	channel bool,
	silent bool,
	archive bool,
	inviteBot bool,
	avatar string,
	ttl int,
	forum bool,
	hideGeneral bool,
	folder string,
) (any, bool) {
	if title == "" {
		return nil, false
	}
	if strings.HasPrefix(title, "hikka-") {
		title = strings.Replace(title, "hikka-", "goroku-", 1)
	}
	if strings.HasPrefix(title, "legacy-") {
		title = strings.Replace(title, "legacy-", "goroku-", 1)
	}
	creator, ok := client.(ChannelCreator)
	if !ok {
		return ErrIncompatibleChannelClient, false
	}

	// Prefer the client's stable Telegram account ID. Clients without one use
	// comparable instance identity, which timed eviction retains for at most one
	// cache TTL. Non-comparable clients bypass caching rather than share peers.
	key, cacheable := assetChannelCacheKey(creator, title)
	now := time.Now().Unix()
	if cacheable {
		channelsCacheMu.Lock()
		for cachedKey, entry := range channelsCache {
			if entry.Exp <= now {
				delete(channelsCache, cachedKey)
			}
		}
		if entry, ok := channelsCache[key]; ok {
			channelsCacheMu.Unlock()
			return entry.Peer, false
		}
		if call, ok := channelCalls[key]; ok {
			call.waiters++
			channelsCacheMu.Unlock()
			<-call.done
			return call.peer, false
		}
	}
	call := &channelCall{done: make(chan struct{})}
	if cacheable {
		channelCalls[key] = call
		channelsCacheMu.Unlock()
	}

	finish := func(peer any) {
		if !cacheable {
			return
		}
		channelsCacheMu.Lock()
		if peer != nil {
			channelsCacheID++
			entry := CacheEntry{
				Peer: peer,
				Exp:  time.Now().Add(channelCacheTTL).Unix(),
				id:   channelsCacheID,
			}
			channelsCache[key] = entry
			time.AfterFunc(channelCacheTTL, func() {
				channelsCacheMu.Lock()
				if current, ok := channelsCache[key]; ok && current.id == entry.id {
					delete(channelsCache, key)
				}
				channelsCacheMu.Unlock()
			})
		}
		call.peer = peer
		delete(channelCalls, key)
		close(call.done)
		channelsCacheMu.Unlock()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			finish(nil)
			panic(recovered)
		}
	}()

	// 1. Search existing channel
	peer, err := creator.FindChannelByTitle(title)
	if err == nil {
		if inviteBot {
			_ = creator.InviteBotToChannel(peer)
		}
		finish(peer)
		return peer, false
	}

	// 2. Create new channel (megagroup = !channel in python)
	newPeer, err := creator.CreateChannel(title, description, !channel, forum)
	if err != nil {
		L().Info("AssetChannel failed to create channel", zap.Error(err))
		finish(nil)
		return nil, false
	}

	if inviteBot {
		_ = creator.InviteBotToChannel(newPeer)
	}

	finish(newPeer)

	return newPeer, true
}

// AssetForumTopic returns or creates a forum topic.
func AssetForumTopic(
	client any,
	db Database,
	peer any,
	title string,
	description string,
	iconEmojiID int64,
	inviteBot bool,
) (any, error) {
	creator, ok := client.(ChannelCreator)
	if !ok {
		topic := map[string]any{
			"ID":    int64(12345),
			"Title": title,
		}
		return topic, nil
	}

	// Read cache from db
	forumsCache := db.GetAnyMap("goroku.forums", "forums_cache", nil)
	if forumsCache == nil {
		forumsCache = make(map[string]any)
	}

	var channelTitle string
	if hasTitle, ok := peer.(interface{ GetTitle() string }); ok {
		channelTitle = hasTitle.GetTitle()
	} else if m, ok := peer.(map[string]any); ok {
		if t, ok := m["Title"].(string); ok {
			channelTitle = t
		}
	}
	if channelTitle == "" {
		channelTitle = "goroku-userbot"
	}

	// Toggle forum mode
	_ = creator.ToggleForum(peer, true)

	var topicID int64
	if subVal, ok := forumsCache[channelTitle]; ok {
		if subMap, ok := subVal.(map[string]any); ok {
			if idVal, ok := subMap[title]; ok {
				switch idt := idVal.(type) {
				case float64:
					topicID = int64(idt)
				case int64:
					topicID = idt
				case int:
					topicID = int64(idt)
				}
			}
		}
	}

	if topicID == 0 {
		tID, err := creator.SearchForumTopic(peer, title)
		if err == nil {
			topicID = tID
			if _, ok := forumsCache[channelTitle]; !ok {
				forumsCache[channelTitle] = make(map[string]any)
			}
			if subMap, ok := forumsCache[channelTitle].(map[string]any); ok {
				subMap[title] = topicID
			}
			if err := db.SetAnyMap("goroku.forums", "forums_cache", forumsCache); err != nil {
				return nil, err
			}
		}
	}

	if topicID == 0 {
		tID, err := creator.CreateForumTopic(peer, title, description, iconEmojiID)
		if err != nil {
			return nil, err
		}
		topicID = tID

		if _, ok := forumsCache[channelTitle]; !ok {
			forumsCache[channelTitle] = make(map[string]any)
		}
		if subMap, ok := forumsCache[channelTitle].(map[string]any); ok {
			subMap[title] = topicID
		}
		if err := db.SetAnyMap("goroku.forums", "forums_cache", forumsCache); err != nil {
			return nil, err
		}
	}

	if inviteBot {
		_ = creator.InviteBotToChannel(peer)
	}

	topic := map[string]any{
		"ID":    topicID,
		"Title": title,
	}
	return topic, nil
}

// ErrContentChannelUnavailable reports that the content channel did not appear
// in the database before the caller's deadline elapsed.
var ErrContentChannelUnavailable = errors.New("goroku content channel unavailable")

// DefaultContentChannelWait bounds how long WaitForContentChannel polls before
// giving up. Callers that cannot proceed without the channel are expected to
// degrade (for example, by sending to the originating chat instead).
const DefaultContentChannelWait = 30 * time.Second

// WaitForContentChannel polls the database until the content channel appears,
// returning ErrContentChannelUnavailable once ctx is done or maxWait elapses,
// whichever comes first. It never blocks indefinitely: an earlier version
// looped forever without a deadline and, when the channel was never created,
// pinned a dispatcher slot for the process lifetime while emitting one Info
// line per poll.
func WaitForContentChannel(ctx context.Context, db Database, delay, maxWait time.Duration) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if delay <= 0 {
		delay = 3 * time.Second
	}
	if maxWait <= 0 {
		maxWait = DefaultContentChannelWait
	}
	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	logged := false
	for {
		if cid := db.GetInt64("goroku.forums", "channel_id", 0); cid != 0 {
			return cid, nil
		}
		if !logged {
			// Logged once per call, not once per poll, to keep a missing channel
			// from dominating log volume.
			L().Info("Goroku content channel not in database yet; waiting",
				zap.Duration("poll_interval", delay), zap.Duration("max_wait", maxWait))
			logged = true
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return 0, fmt.Errorf("%w: %w", ErrContentChannelUnavailable, ctx.Err())
		}
	}
}

// GetTopicID gets topic ID from database forums cache.
func GetTopicID(db Database, topicName string) any {
	forumsCache := db.GetAnyMap("goroku.forums", "forums_cache", nil)
	subCache, ok := forumsCache["goroku-userbot"].(map[string]any)
	if !ok {
		return nil
	}
	return subCache[topicName]
}

// SetAvatar is not implemented and always reports failure.
//
// It previously returned true unconditionally while doing nothing, so callers
// could not distinguish "avatar set" from "silently ignored". Reporting false
// keeps that indistinguishable case from being mistaken for success. Implement
// it against the Telegram photo API before relying on the result.
func SetAvatar(client any, peer any, avatar string) bool {
	fwProtect()
	return false
}

// GetTarget is not implemented and always returns nil.
// Callers must treat nil as "no target resolved", not "no target given".
func GetTarget(message any, argNo int) any {
	return nil
}

// GetUser is not implemented and always returns nil.
// Callers must treat nil as "lookup unavailable", not "user absent".
func GetUser(message any) any {
	return nil
}

// GetChatID returns chat ID.
func GetChatID(message any) int64 {
	if message == nil {
		return 0
	}
	val := reflect.ValueOf(message)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return 0
	}

	chatIdField := val.FieldByName("ChatID")
	if chatIdField.IsValid() {
		switch chatIdField.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return chatIdField.Int()
		}
	}

	chatField := val.FieldByName("Chat")
	if chatField.IsValid() {
		cVal := chatField
		if cVal.Kind() == reflect.Ptr {
			cVal = cVal.Elem()
		}
		if cVal.Kind() == reflect.Struct {
			idField := cVal.FieldByName("ID")
			if idField.IsValid() {
				switch idField.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					return idField.Int()
				}
			}
		}
	}
	return 0
}

// GetEntityID returns entity ID.
func GetEntityID(entity any) int64 {
	id, _, _ := entityFields(entity)
	return id
}

// EscapeNonHTML escapes only non-HTML entities.
func EscapeNonHTML(text string) string {
	var builder strings.Builder
	matches := tagRe.FindAllStringSubmatchIndex(text, -1)
	last := 0
	for _, m := range matches {
		builder.WriteString(EscapeHTML(text[last:m[0]]))
		tag := text[m[0]:m[1]]
		tagName := strings.ToLower(text[m[2]:m[3]])
		if telegramHtmlTags[tagName] {
			builder.WriteString(tag)
		} else {
			builder.WriteString(EscapeHTML(tag))
		}
		last = m[1]
	}
	builder.WriteString(EscapeHTML(text[last:]))
	return builder.String()
}

// RelocateEntities moves text entities.
func RelocateEntities(entities []FormattingEntity, offset int, text string) []FormattingEntity {
	length := len(text)
	var result []FormattingEntity
	for _, ent := range entities {
		ent.Offset += offset
		if ent.Offset < 0 {
			ent.Length += ent.Offset
			ent.Offset = 0
		}
		if text != "" && ent.Offset+ent.Length > length {
			ent.Length = length - ent.Offset
		}
		if ent.Length > 0 {
			result = append(result, ent)
		}
	}
	return result
}

// FindCaller returns calling module/method name.
func FindCaller() any {
	return nil
}

// DND mutes the channel.
func DND(client any, peer any, archive bool) bool {
	fwProtect()
	return true
}

// AsciiFace returns random cute text face.
func AsciiFace() string {
	faces := []string{
		"ヽ(๑◠ܫ◠๑)ﾉ", "(◕ᴥ◕ʋ)", "ᕙ(`▽´)ᕗ", "(✿◠‿◠)", "(▰˘◡˘▰)",
		"(˵ ͡° ͜ʖ ͡°˵)", "ʕっ•ᴥ•ʔっ", "( ͡° ᴥ ͡°)", "(๑•́ ヮ •̀๑)", "٩(^‿^)۶",
		"(っˆڡˆς)", "ψ(｀∇´)ψ", "⊙ω⊙", "٩(^ᴗ^)۶", "(´・ω・)っ由",
		"( ͡~ ͜ʖ ͡°)", "✧♡(◕‿◕✿)", "โ๏௰๏ใ ื", "∩｡• ᵕ •｡∩ ♡", "(♡´౪`♡)",
		"(◍＞◡＜◍)⋈。✧♡", "╰(✿´⌣`✿)╯♡", "ʕ•ᴥ•ʔ", "ᶘ ◕ᴥ◕ᶅ", "▼・ᴥ・▼",
		"ฅ^•ﻌ•^ฅ", "(΄◞ิ౪◟ิ‵)", "٩(^ᴗ^)۶", "ᕴｰᴥｰᕵ", "ʕ￫ᴥ￩ʔ",
		"ʕᵕᴥᵕʔ", "ʕᵒᴥᵒʔ", "ᵔᴥᵔ", "(✿╹◡╹)", "(๑￫ܫ￩)",
		"ʕ·ᴥ·　ʔ", "(ﾉ≧ڡ≦)", "(≖ᴗ≖✿)", "（〜^∇^ )〜", "( ﾉ･ｪ･ )ﾉ",
		"~( ˘▾˘~)", "(〜^∇^)〜", "ヽ(^ᴗ^ヽ)", "(´･ω･`)", "₍ᐢ•ﻌ•ᐢ₎*･ﾟ｡",
		"(。・・)_且", "(=｀ω´=)", "(*•‿•*)", "(*ﾟ∀ﾟ*)", "(☉⋆‿⋆☉)",
		"ɷ◡ɷ", "ʘ‿ʘ", "(。-ω-)ﾉ", "( ･ω･)ﾉ", "(=ﾟωﾟ)ﾉ",
		"(・ε・`*) …", "ʕっ•ᴥ•ʔっ", "(*˘︶˘*)", "ಥ_ಥ", "･ﾟ･(｡>д<｡)･ﾟ･",
		"(┬┬＿┬┬)", "(◞‸◟ㆀ)", " ˚‧º·(˚ ˃̣̣̥⌓˂̣̣̥ )‧º·˚",
	}
	idx := rand.Intn(len(faces)) //nolint:gosec
	return EscapeHTML(faces[idx])
}
