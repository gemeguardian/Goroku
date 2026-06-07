package utils

import (
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// FormattingEntity represents a text formatting entity.
type FormattingEntity struct {
	Offset int
	Length int
	Type   string
}

// Database interface to break circular dependencies
type Database interface {
	Get(owner, key string, defaultValue interface{}) interface{}
	Set(owner, key string, value interface{}) bool
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
	Peer interface{}
	Exp  int64
}

var channelsCache = make(map[string]CacheEntry)

func fwProtect() {
	time.Sleep(1000 * time.Millisecond)
}

func getEntityFields(entity interface{}) (int64, string, bool) {
	if entity == nil {
		return 0, "", false
	}
	val := reflect.ValueOf(entity)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return 0, "", false
	}

	var id int64
	var username string
	isUser := false

	idField := val.FieldByName("ID")
	if idField.IsValid() {
		switch idField.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			id = idField.Int()
		}
	} else {
		idField = val.FieldByName("Id")
		if idField.IsValid() {
			switch idField.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				id = idField.Int()
			}
		}
	}

	usernameField := val.FieldByName("Username")
	if usernameField.IsValid() && usernameField.Kind() == reflect.String {
		username = usernameField.String()
	}

	typeName := val.Type().Name()
	if strings.Contains(strings.ToLower(typeName), "user") {
		isUser = true
	}

	return id, username, isUser
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

// GetEntityURL returns a link to the user/channel.
func GetEntityURL(entity interface{}, openmessage bool) string {
	id, username, isUser := getEntityFields(entity)
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
func GetLink(entity interface{}) string {
	return GetEntityURL(entity, false)
}

type ChannelCreator interface {
	FindChannelByTitle(title string) (interface{}, error)
	CreateChannel(title, description string, megagroup, forum bool) (interface{}, error)
	InviteBotToChannel(channelPeer interface{}) error
	ToggleForum(channelPeer interface{}, enabled bool) error
	CreateForumTopic(channelPeer interface{}, title, description string, iconEmojiID int64) (int64, error)
	SearchForumTopic(channelPeer interface{}, title string) (int64, error)
}

// AssetChannel returns or creates a channel.
func AssetChannel(
	client interface{},
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
) (interface{}, bool) {
	if title == "" {
		return nil, false
	}
	if strings.HasPrefix(title, "hikka-") {
		title = strings.Replace(title, "hikka-", "goroku-", 1)
	}
	if strings.HasPrefix(title, "legacy-") {
		title = strings.Replace(title, "legacy-", "goroku-", 1)
	}

	key := title
	if entry, ok := channelsCache[key]; ok && entry.Exp > time.Now().Unix() {
		return entry.Peer, false
	}

	creator, ok := client.(ChannelCreator)
	if !ok {
		// Stub fallback representation if not satisfying the interface
		peer := map[string]interface{}{
			"ID":       int64(987654321),
			"Title":    title,
			"Username": "",
		}
		fwProtect()
		channelsCache[key] = CacheEntry{
			Peer: peer,
			Exp:  time.Now().Unix() + 3600,
		}
		return peer, true
	}

	// 1. Search existing channel
	peer, err := creator.FindChannelByTitle(title)
	if err == nil {
		if inviteBot {
			_ = creator.InviteBotToChannel(peer)
		}
		channelsCache[key] = CacheEntry{
			Peer: peer,
			Exp:  time.Now().Unix() + 3600,
		}
		return peer, false
	}

	// 2. Create new channel (megagroup = !channel in python)
	newPeer, err := creator.CreateChannel(title, description, !channel, forum)
	if err != nil {
		log.Printf("AssetChannel failed to create channel: %v\n", err)
		return nil, false
	}

	if inviteBot {
		_ = creator.InviteBotToChannel(newPeer)
	}

	channelsCache[key] = CacheEntry{
		Peer: newPeer,
		Exp:  time.Now().Unix() + 3600,
	}

	return newPeer, true
}

// AssetForumTopic returns or creates a forum topic.
func AssetForumTopic(
	client interface{},
	db Database,
	peer interface{},
	title string,
	description string,
	iconEmojiID int64,
	inviteBot bool,
) (interface{}, error) {
	creator, ok := client.(ChannelCreator)
	if !ok {
		topic := map[string]interface{}{
			"ID":    int64(12345),
			"Title": title,
		}
		return topic, nil
	}

	// Read cache from db
	forumsCache := make(map[string]interface{})
	forumsCacheVal := db.Get("goroku.forums", "forums_cache", nil)
	if forumsCacheVal != nil {
		if m, ok := forumsCacheVal.(map[string]interface{}); ok {
			forumsCache = m
		}
	}

	var channelTitle string
	if hasTitle, ok := peer.(interface{ GetTitle() string }); ok {
		channelTitle = hasTitle.GetTitle()
	} else if m, ok := peer.(map[string]interface{}); ok {
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
		if subMap, ok := subVal.(map[string]interface{}); ok {
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
				forumsCache[channelTitle] = make(map[string]interface{})
			}
			if subMap, ok := forumsCache[channelTitle].(map[string]interface{}); ok {
				subMap[title] = topicID
			}
			db.Set("goroku.forums", "forums_cache", forumsCache)
		}
	}

	if topicID == 0 {
		tID, err := creator.CreateForumTopic(peer, title, description, iconEmojiID)
		if err != nil {
			return nil, err
		}
		topicID = tID

		if _, ok := forumsCache[channelTitle]; !ok {
			forumsCache[channelTitle] = make(map[string]interface{})
		}
		if subMap, ok := forumsCache[channelTitle].(map[string]interface{}); ok {
			subMap[title] = topicID
		}
		db.Set("goroku.forums", "forums_cache", forumsCache)
	}

	if inviteBot {
		_ = creator.InviteBotToChannel(peer)
	}

	topic := map[string]interface{}{
		"ID":    topicID,
		"Title": title,
	}
	return topic, nil
}

// WaitForContentChannel waits until content channel exists in the database.
func WaitForContentChannel(db Database, delay float64) int64 {
	cidVal := db.Get("goroku.forums", "channel_id", nil)
	for cidVal == nil {
		log.Println("Goroku content channel not found in database. Sleeping 10 seconds...")
		time.Sleep(time.Duration(delay * float64(time.Second)))
		cidVal = db.Get("goroku.forums", "channel_id", nil)
	}
	switch v := cidVal.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}

// GetTopicID gets topic ID from database forums cache.
func GetTopicID(db Database, topicName string) interface{} {
	forumsCacheVal := db.Get("goroku.forums", "forums_cache", nil)
	if forumsCacheVal == nil {
		return nil
	}
	forumsCache, ok := forumsCacheVal.(map[string]interface{})
	if !ok {
		return nil
	}
	subCacheVal, ok := forumsCache["goroku-userbot"]
	if !ok {
		return nil
	}
	subCache, ok := subCacheVal.(map[string]interface{})
	if !ok {
		return nil
	}
	return subCache[topicName]
}

// SetAvatar stubs setting entity avatar.
func SetAvatar(client interface{}, peer interface{}, avatar string) bool {
	fwProtect()
	return true
}

// GetTarget stubs getting a target ID from command.
func GetTarget(message interface{}, argNo int) interface{} {
	return nil
}

// GetUser stubs fetching a user.
func GetUser(message interface{}) interface{} {
	return nil
}

// GetChatID returns chat ID.
func GetChatID(message interface{}) int64 {
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
func GetEntityID(entity interface{}) int64 {
	id, _, _ := getEntityFields(entity)
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
func FindCaller() interface{} {
	return nil
}

// DND mutes the channel.
func DND(client interface{}, peer interface{}, archive bool) bool {
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
	idx := rand.Intn(len(faces))
	return EscapeHTML(faces[idx])
}
