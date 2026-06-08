package goroku

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

var (
	ruRunes = []rune("ёйцукенгшщзхъфывапролджэячсмитьбю.Ё\"№;%:?ЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭ/ЯЧСМИТЬБЮ,")
	enRunes = []rune("`qwertyuiop[]asdfghjkl;'zxcvbnm,./~@#$%^&QWERTYUIOP{}ASDFGHJKL:\"|ZXCVBNM<>?")
)

var layoutTranslation = func() map[rune]rune {
	m := make(map[rune]rune)
	for i := 0; i < len(ruRunes) && i < len(enRunes); i++ {
		m[ruRunes[i]] = enRunes[i]
		m[enRunes[i]] = ruRunes[i]
	}
	return m
}()

func translateLayout(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if t, exists := layoutTranslation[r]; exists {
			runes[i] = t
		}
	}
	return string(runes)
}

type RatelimitedModule interface {
	RatelimitedCommands() map[string]bool
}

type CommandDispatcher struct {
	mu                   sync.RWMutex
	modules              *Modules
	client               *CustomTelegramClient
	db                   *Database
	ratelimitStorageUser map[int64]int
	ratelimitStorageChat map[int64]int
	ratelimitMaxUser     int
	ratelimitMaxChat     int
	security             *SecurityManager
	me                   int64
	cachedUsernames      map[string]bool
}

func NewCommandDispatcher(modules *Modules, client *CustomTelegramClient, db *Database) *CommandDispatcher {
	maxUser := 30
	maxChat := 100

	if val, ok := db.Get("goroku.dispatcher", "ratelimit_max_user", 30).(float64); ok {
		maxUser = int(val)
	}
	if val, ok := db.Get("goroku.dispatcher", "ratelimit_max_chat", 100).(float64); ok {
		maxChat = int(val)
	}

	cd := &CommandDispatcher{
		modules:              modules,
		client:               client,
		db:                   db,
		ratelimitStorageUser: make(map[int64]int),
		ratelimitStorageChat: make(map[int64]int),
		ratelimitMaxUser:     maxUser,
		ratelimitMaxChat:     maxChat,
		me:                   client.TGID,
		cachedUsernames:      make(map[string]bool),
		security:             NewSecurityManager(client, db),
	}

	if client.Username != "" {
		cd.cachedUsernames[strings.ToLower(client.Username)] = true
	}
	cd.cachedUsernames[strconv.FormatInt(client.TGID, 10)] = true

	return cd
}

// GetSecurityManager returns the security manager for external use by modules.
func (cd *CommandDispatcher) GetSecurityManager() *SecurityManager {
	return cd.security
}

func (cd *CommandDispatcher) HandleIncoming(msg *Message) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	// Check blacklists
	blacklistChats := cd.getBlacklistChats()
	chatStr := strconv.FormatInt(msg.ChatID, 10)
	if blacklistChats[chatStr] {
		return
	}

	// Check whitelist chats
	whitelistChatsVal := cd.db.Get("goroku.main", "whitelist_chats", []interface{}{})
	var whitelistChats []int64
	if slice, ok := whitelistChatsVal.([]interface{}); ok {
		for _, item := range slice {
			switch v := item.(type) {
			case float64:
				whitelistChats = append(whitelistChats, int64(v))
			case string:
				id, _ := strconv.ParseInt(v, 10, 64)
				whitelistChats = append(whitelistChats, id)
			}
		}
	}

	if len(whitelistChats) > 0 {
		found := false
		for _, wChat := range whitelistChats {
			if wChat == msg.ChatID {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}

	// Check whitelist modules
	whitelistModulesVal := cd.db.Get("goroku.main", "whitelist_modules", []interface{}{})
	var whitelistModules []string
	if slice, ok := whitelistModulesVal.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				whitelistModules = append(whitelistModules, s)
			}
		}
	}

	// Retrieve disabled watchers
	disabledWatchersVal := cd.db.Get("goroku.main", "disabled_watchers", map[string]interface{}{})
	disabledWatchers := map[string]interface{}{}
	if dw, ok := disabledWatchersVal.(map[string]interface{}); ok {
		disabledWatchers = dw
	}

	// Dispatch message watchers
	for _, watcher := range cd.modules.watchers {
		modName := watcher.ModuleName

		// Check if this module's watchers are disabled
		if wl, exists := disabledWatchers[modName]; exists {
			if slice, ok := wl.([]interface{}); ok {
				disabledHere := false
				for _, item := range slice {
					valStr := fmt.Sprintf("%v", item)
					if valStr == "*" {
						disabledHere = true
						break
					}
					// Check specific chat blacklist for watcher
					if valStr == chatStr {
						disabledHere = true
						break
					}
					if valStr == "only_chats" && msg.IsPrivate {
						disabledHere = true
						break
					}
					if valStr == "only_pm" && !msg.IsPrivate {
						disabledHere = true
						break
					}
					if valStr == "out" && !msg.Out {
						disabledHere = true
						break
					}
					if valStr == "in" && msg.Out {
						disabledHere = true
						break
					}
				}
				if disabledHere {
					continue
				}
			}
		}

		// Check blacklist chats with specific module (chat_id.module_name)
		key1 := fmt.Sprintf("%s.%s", chatStr, modName)
		key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
		if blacklistChats[key1] || blacklistChats[key2] {
			continue
		}

		// Check whitelist modules (chat_id.module_name)
		if len(whitelistModules) > 0 {
			found := false
			for _, wm := range whitelistModules {
				if wm == key1 || wm == key2 {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		if !cd.watcherTagsMatch(msg, watcher.Meta) {
			continue
		}

		go func(w WatcherHandler, m *Message) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Watcher panic recovered: %v\n", r)
				}
			}()
			_ = w(m)
		}(watcher.Handler, msg)
	}
}

func (cd *CommandDispatcher) watcherTagsMatch(msg *Message, meta CommandMeta) bool {
	isCommand := cd.isCommandMessage(msg)
	if meta.NoCommands && isCommand {
		return false
	}
	if meta.OnlyCommands && !isCommand {
		return false
	}
	if meta.OnlyPM && !msg.IsPrivate {
		return false
	}
	if meta.NoPM && msg.IsPrivate {
		return false
	}
	if meta.OnlyChats && msg.IsPrivate {
		return false
	}
	if meta.OnlyGroups && !msg.IsGroup {
		return false
	}
	if meta.OnlyChannels && !msg.IsChannel {
		return false
	}
	if meta.NoForwarded && msg.IsForwarded {
		return false
	}
	if meta.Regex != "" {
		re, err := regexp.Compile(meta.Regex)
		if err != nil || !re.MatchString(msg.RawText) {
			return false
		}
	}
	if meta.StartsWith != "" && !strings.HasPrefix(msg.RawText, meta.StartsWith) {
		return false
	}
	if meta.EndsWith != "" && !strings.HasSuffix(msg.RawText, meta.EndsWith) {
		return false
	}
	if meta.Contains != "" && !strings.Contains(msg.RawText, meta.Contains) {
		return false
	}
	if len(meta.FromID) > 0 {
		ok := false
		for _, id := range meta.FromID {
			if id == msg.SenderID {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(meta.ChatID) > 0 {
		ok := false
		for _, id := range meta.ChatID {
			if id == msg.ChatID {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if meta.Filter != nil && !meta.Filter(msg) {
		return false
	}
	return true
}

func (cd *CommandDispatcher) isCommandMessage(msg *Message) bool {
	text := strings.TrimSpace(msg.RawText)
	if text == "" {
		return false
	}
	prefix := cd.getPrefix(msg.SenderID)
	if prefix != "" && strings.HasPrefix(text, prefix) {
		return true
	}
	return false
}

func (cd *CommandDispatcher) handleTags(msg *Message, cmdName string) bool {
	var meta CommandMeta
	found := false
	for _, mod := range cd.modules.GetModules() {
		if _, exists := mod.Commands()[cmdName]; exists {
			if withMeta, ok := mod.(ModuleWithMeta); ok {
				if m, exists := withMeta.CommandMetas()[cmdName]; exists {
					meta = m
					found = true
				}
			}
			break
		}
	}

	if !found {
		return true
	}

	if meta.OnlyOwner && !cd.security.Check(msg, "owner") {
		return false
	}
	if meta.OnlyPM && !msg.IsPrivate {
		return false
	}
	if meta.NoPM && msg.IsPrivate {
		return false
	}
	if meta.OnlyChats && msg.IsPrivate {
		return false
	}
	if meta.OnlyGroups && !msg.IsGroup {
		return false
	}
	if meta.OnlyChannels && !msg.IsChannel {
		return false
	}
	if meta.NoForwarded && msg.IsForwarded {
		return false
	}
	if meta.NoReply && msg.ReplyToMsgID != 0 {
		return false
	}
	if meta.OnlyReply && msg.ReplyToMsgID == 0 {
		return false
	}
	if len(meta.FromID) > 0 {
		senderFound := false
		for _, id := range meta.FromID {
			if id == msg.SenderID {
				senderFound = true
				break
			}
		}
		if !senderFound {
			return false
		}
	}
	if len(meta.ChatID) > 0 {
		chatFound := false
		for _, id := range meta.ChatID {
			if id == msg.ChatID {
				chatFound = true
				break
			}
		}
		if !chatFound {
			return false
		}
	}
	if meta.Regex != "" {
		re, err := regexp.Compile(meta.Regex)
		if err != nil || !re.MatchString(msg.RawText) {
			return false
		}
	}
	if meta.StartsWith != "" && !strings.HasPrefix(msg.RawText, meta.StartsWith) {
		return false
	}
	if meta.EndsWith != "" && !strings.HasSuffix(msg.RawText, meta.EndsWith) {
		return false
	}
	if meta.Contains != "" && !strings.Contains(msg.RawText, meta.Contains) {
		return false
	}

	return true
}

func (cd *CommandDispatcher) HandleCommand(msg *Message) {
	if msg.Text == "" {
		return
	}

	prefix := cd.getPrefix(msg.SenderID)

	// Layout auto-correction check
	translatedPrefix := translateLayout(prefix)
	msgText := msg.Text

	if strings.HasPrefix(msgText, translatedPrefix) && translatedPrefix != prefix {
		msgText = translateLayout(msgText)
	}

	if !strings.HasPrefix(msgText, prefix) {
		return
	}

	if strings.HasPrefix(msgText, prefix+prefix) {
		// Python: message.edit(message.message[len(prefix):])
		// — edit the same message, stripping one prefix, do NOT send a new message
		cleaned := msgText[len(prefix):]
		if msg.Out {
			_, _ = cd.client.EditMessage(msg.ChatID, msg.ID, cleaned)
		} else {
			_, _ = cd.client.SendMessage(msg.ChatID, cleaned)
		}
		return
	}

	// Skip stickers, dice, audio messages and via_bot messages (like Python dispatcher)
	if msg.Media != nil {
		switch msg.Media.(type) {
		case *tg.MessageMediaDice, *tg.MessageMediaGame:
			return
		}
	}
	// Skip forwarded-from-bot messages (via_bot_id equivalent)
	if msg.IsForwarded {
		if fwd, ok := msg.FwdFrom.(tg.MessageFwdHeader); ok && fwd.FromID != nil {
			if _, isChannel := fwd.FromID.(*tg.PeerChannel); isChannel {
				// forwarded from channel — allow, it's normal
			}
		}
	}

	// Check blacklisted chats
	blacklistChats := cd.getBlacklistChats()
	chatStr := strconv.FormatInt(msg.ChatID, 10)
	if blacklistChats[chatStr] {
		return
	}

	// Check whitelist chats
	whitelistChatsVal := cd.db.Get("goroku.main", "whitelist_chats", []interface{}{})
	var whitelistChats []int64
	if slice, ok := whitelistChatsVal.([]interface{}); ok {
		for _, item := range slice {
			switch v := item.(type) {
			case float64:
				whitelistChats = append(whitelistChats, int64(v))
			case string:
				id, _ := strconv.ParseInt(v, 10, 64)
				whitelistChats = append(whitelistChats, id)
			}
		}
	}

	if len(whitelistChats) > 0 {
		found := false
		for _, wChat := range whitelistChats {
			if wChat == msg.ChatID {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}

	// Extract command name
	cmdBody := msgText[len(prefix):]
	parts := strings.Fields(cmdBody)
	if len(parts) == 0 {
		return
	}

	commandName := parts[0]
	tagParts := strings.Split(commandName, "@")

	// Target check (e.g. .help@my_bot)
	if len(tagParts) == 2 {
		target := strings.ToLower(tagParts[1])
		if target == "me" {
			if !msg.Out {
				return
			}
		} else if !cd.cachedUsernames[target] {
			return
		}
	}

	actualCmd := tagParts[0]
	handler, exists := cd.modules.Dispatch(actualCmd)
	if !exists {
		log.Printf("[Dispatcher] Command %q not found in registry\n", actualCmd)
		return
	}

	// Find which module owns this command
	var modName string
	for _, mod := range cd.modules.GetModules() {
		if _, exists := mod.Commands()[actualCmd]; exists {
			modName = mod.Name()
			break
		}
	}

	// Check blacklist chats with specific module (chat_id.module_name)
	if modName != "" {
		key1 := fmt.Sprintf("%s.%s", chatStr, modName)
		key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
		if blacklistChats[key1] || blacklistChats[key2] {
			return
		}
	}

	// Check whitelist modules (chat_id.module_name)
	whitelistModulesVal := cd.db.Get("goroku.main", "whitelist_modules", []interface{}{})
	var whitelistModules []string
	if slice, ok := whitelistModulesVal.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				whitelistModules = append(whitelistModules, s)
			}
		}
	}

	if len(whitelistModules) > 0 && modName != "" {
		found := false
		key1 := fmt.Sprintf("%s.%s", chatStr, modName)
		key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
		for _, wm := range whitelistModules {
			if wm == key1 || wm == key2 {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}

	// Nickname check
	if !msg.Out && !msg.IsPrivate {
		// Check if mentioned
		mentioned := false
		if cd.client.Username != "" && strings.Contains(strings.ToLower(msg.Text), "@"+strings.ToLower(cd.client.Username)) {
			mentioned = true
		}

		if !mentioned {
			noNicknameVal := cd.db.Get("goroku.main", "no_nickname", false)
			noNickname := false
			if v, ok := noNicknameVal.(bool); ok {
				noNickname = v
			}

			if !noNickname {
				// Check nonickcmds
				nonickcmdsVal := cd.db.Get("goroku.main", "nonickcmds", []interface{}{})
				cmdWhitelisted := false
				if slice, ok := nonickcmdsVal.([]interface{}); ok {
					for _, item := range slice {
						if s, ok := item.(string); ok && strings.EqualFold(s, actualCmd) {
							cmdWhitelisted = true
							break
						}
					}
				}

				// Check nonickusers
				nonickusersVal := cd.db.Get("goroku.main", "nonickusers", []interface{}{})
				userWhitelisted := false
				if slice, ok := nonickusersVal.([]interface{}); ok {
					for _, item := range slice {
						var uid int64
						switch v := item.(type) {
						case float64:
							uid = int64(v)
						case int64:
							uid = v
						}
						if uid == msg.SenderID {
							userWhitelisted = true
							break
						}
					}
				}

				// Check nonickchats
				nonickchatsVal := cd.db.Get("goroku.main", "nonickchats", []interface{}{})
				chatWhitelisted := false
				if slice, ok := nonickchatsVal.([]interface{}); ok {
					for _, item := range slice {
						var cid int64
						switch v := item.(type) {
						case float64:
							cid = int64(v)
						case int64:
							cid = v
						}
						if cid == msg.ChatID {
							chatWhitelisted = true
							break
						}
					}
				}

				// Check tsec rules
				tsecWhitelisted := cd.security.CheckTsec(msg.SenderID, actualCmd)

				if !cmdWhitelisted && !userWhitelisted && !chatWhitelisted && !tsecWhitelisted {
					// Nickname checks are enabled, and this command is not whitelisted in any way, so ignore it
					log.Printf("[Dispatcher] Nickname check failed for cmd=%q, ignoring\n", actualCmd)
					return
				}
			}
		}
	}

	// Check if the command's module is disabled
	if cd.isModuleOrCommandDisabled(actualCmd) {
		log.Printf("[Dispatcher] Command %q or its module is disabled, ignoring\n", actualCmd)
		return
	}

	// Check security level
	if !cd.security.Check(msg, actualCmd) {
		log.Printf("[Dispatcher] Security check failed for cmd=%q, ignoring\n", actualCmd)
		return
	}

	// Check tag filters
	if !cd.handleTags(msg, actualCmd) {
		log.Printf("[Dispatcher] Tag filter failed for cmd=%q, ignoring\n", actualCmd)
		return
	}

	// Check rate limit
	if !cd.handleRatelimit(msg, actualCmd) {
		log.Printf("[Dispatcher] Rate limit exceeded for cmd: %s in chat: %d\n", actualCmd, msg.ChatID)
		return
	}

	// Grep pipeline check
	msg = cd.handleGrep(msg)

	// Execute command handler asynchronously
	go func(h CommandHandler, m *Message) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Command panic recovered: %v\n", r)
				_ = m.Answer(fmt.Sprintf("❌ <b>Command crashed! Panic:</b> <code>%v</code>", r))
			}
		}()
		log.Printf("[Dispatcher] Dispatching command %q to handler...\n", actualCmd)
		originalText := m.Text
		err := h(m)
		if err != nil {
			log.Printf("[Dispatcher] Command %q failed with error: %v\n", actualCmd, err)
			_ = m.Answer(fmt.Sprintf("❌ <b>Command execution error:</b> <code>%s</code>", err.Error()))
		} else {
			log.Printf("[Dispatcher] Command %q completed successfully. Answered=%t, TextChanged=%t\n", actualCmd, m.Answered, m.Text != originalText)
			if !m.Answered && m.Text != originalText {
				if err := m.Answer(m.Text); err != nil {
					log.Printf("[Dispatcher] Auto-answer for command %q failed: %v\n", actualCmd, err)
				}
			}
		}
	}(handler, msg)
}

func (cd *CommandDispatcher) handleRatelimit(msg *Message, cmdName string) bool {
	// If owner, bypass rate limits completely
	if cd.security.Check(msg, "owner") {
		return true
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	ratelimit := false
	for _, mod := range cd.modules.GetModules() {
		if _, exists := mod.Commands()[cmdName]; exists {
			if rlMod, ok := mod.(RatelimitedModule); ok {
				if rlMod.RatelimitedCommands()[cmdName] {
					ratelimit = true
				}
			}
			break
		}
	}

	ret := true
	chat := cd.ratelimitStorageChat[msg.ChatID]
	var severity int

	if msg.SenderID != 0 {
		user := cd.ratelimitStorageUser[msg.SenderID]
		baseSev := 2
		if ratelimit {
			baseSev = 5
		}
		severity = baseSev * ((user+chat)/30 + 1)
		user += severity
		cd.ratelimitStorageUser[msg.SenderID] = user

		if user > cd.ratelimitMaxUser {
			ret = false
		} else {
			cd.ratelimitStorageChat[msg.ChatID] = chat
		}

		// Decrement user rate limit after self.ratelimitMaxUser * severity seconds
		go func(senderID int64, sev int) {
			delay := time.Duration(cd.ratelimitMaxUser*sev) * time.Second
			time.Sleep(delay)
			cd.mu.Lock()
			defer cd.mu.Unlock()
			cd.ratelimitStorageUser[senderID] = cd.ratelimitStorageUser[senderID] - sev
			if cd.ratelimitStorageUser[senderID] < 0 {
				cd.ratelimitStorageUser[senderID] = 0
			}
		}(msg.SenderID, severity)
	} else {
		baseSev := 2
		if ratelimit {
			baseSev = 5
		}
		severity = baseSev * (chat/15 + 1)
	}

	chat += severity
	cd.ratelimitStorageChat[msg.ChatID] = chat

	if chat > cd.ratelimitMaxChat {
		ret = false
	}

	// Decrement chat rate limit after self.ratelimitMaxChat * severity seconds
	go func(chatID int64, sev int) {
		delay := time.Duration(cd.ratelimitMaxChat*sev) * time.Second
		time.Sleep(delay)
		cd.mu.Lock()
		defer cd.mu.Unlock()
		cd.ratelimitStorageChat[chatID] = cd.ratelimitStorageChat[chatID] - sev
		if cd.ratelimitStorageChat[chatID] < 0 {
			cd.ratelimitStorageChat[chatID] = 0
		}
	}(msg.ChatID, severity)

	return ret
}

func (cd *CommandDispatcher) handleGrep(msg *Message) *Message {
	// Parse python grep filters: message text containing `| grep query` or `| grep -v query`
	re := regexp.MustCompile(`\| ?grep (.+)`)
	loc := re.FindStringSubmatch(msg.RawText)
	if len(loc) == 2 {
		query := strings.TrimSpace(loc[1])
		invert := false
		if strings.HasPrefix(query, "-v ") {
			invert = true
			query = strings.TrimSpace(query[3:])
		}

		// Wipe pipeline arguments from message text representation
		cleaned := re.ReplaceAllString(msg.Text, "")
		msg.Text = cleaned
		msg.RawText = cleaned

		msg.GrepQuery = query
		msg.GrepInvert = invert
	}

	// Parse | cut N — keep first N lines of output
	reCut := regexp.MustCompile(`\| ?cut (\d+)`)
	if loc := reCut.FindStringSubmatch(msg.RawText); len(loc) == 2 {
		n, _ := strconv.Atoi(loc[1])
		msg.CutLines = n
		msg.Text = reCut.ReplaceAllString(msg.Text, "")
		msg.RawText = reCut.ReplaceAllString(msg.RawText, "")
	}

	// Parse | split — send output as multiple messages
	if strings.Contains(msg.RawText, "| split") {
		msg.SplitOutput = true
		msg.Text = strings.ReplaceAll(msg.Text, "| split", "")
		msg.RawText = strings.ReplaceAll(msg.RawText, "| split", "")
	}

	return msg
}

func (cd *CommandDispatcher) isModuleOrCommandDisabled(cmdName string) bool {
	// Check disabled_modules
	disabledModsVal := cd.db.Get("goroku.main", "disabled_modules", []interface{}{})
	disabledMods := []string{}
	if slice, ok := disabledModsVal.([]interface{}); ok {
		for _, v := range slice {
			if s, ok := v.(string); ok {
				disabledMods = append(disabledMods, s)
			}
		}
	}

	// Find which module owns this command
	for _, mod := range cd.modules.GetModules() {
		if _, exists := mod.Commands()[cmdName]; exists {
			modName := mod.Name()
			// Check if module is disabled
			for _, dm := range disabledMods {
				if dm == modName {
					return true
				}
			}
			// Check disabled_commands
			disabledCmdsVal := cd.db.Get("goroku.main", "disabled_commands", map[string]interface{}{})
			if dcMap, ok := disabledCmdsVal.(map[string]interface{}); ok {
				if cmdList, ok := dcMap[modName]; ok {
					if bytes, err := json.Marshal(cmdList); err == nil {
						var cmds []string
						if json.Unmarshal(bytes, &cmds) == nil {
							for _, dc := range cmds {
								if strings.EqualFold(dc, cmdName) {
									return true
								}
							}
						}
					}
				}
			}
			break
		}
	}
	return false
}

func (cd *CommandDispatcher) getPrefix(senderID int64) string {
	mainPrefixVal := cd.db.Get("goroku.main", "command_prefix", ".")
	mainPrefix := "."
	if val, ok := mainPrefixVal.(string); ok {
		mainPrefix = val
	}

	if senderID == cd.client.TGID {
		return mainPrefix
	}

	prefixesVal := cd.db.Get("goroku.main", "command_prefixes", make(map[string]interface{}))
	if prefixes, ok := prefixesVal.(map[string]interface{}); ok {
		senderStr := strconv.FormatInt(senderID, 10)
		if customPrefix, exists := prefixes[senderStr].(string); exists {
			return customPrefix
		}
	}

	return mainPrefix
}

func (cd *CommandDispatcher) getBlacklistChats() map[string]bool {
	res := make(map[string]bool)
	val := cd.db.Get("goroku.main", "blacklist_chats", []interface{}{})
	if slice, ok := val.([]interface{}); ok {
		for _, item := range slice {
			switch v := item.(type) {
			case float64:
				res[strconv.FormatInt(int64(v), 10)] = true
			case string:
				res[v] = true
			}
		}
	}
	return res
}

// HandleInlineQuery handles incoming MTProto inline queries for the bot
func (cd *CommandDispatcher) HandleInlineQuery(query *tg.UpdateBotInlineQuery) {
	log.Printf("Received inline query: %s from user: %d\n", query.Query, query.UserID)
	// Placeholder for routing to inline handlers
}

// HandleCallbackQuery handles incoming MTProto callback queries for the bot
func (cd *CommandDispatcher) HandleCallbackQuery(query *tg.UpdateBotCallbackQuery) {
	log.Printf("Received callback query data: %s from user: %d\n", string(query.Data), query.UserID)
	// Placeholder for routing to callback handlers
}
