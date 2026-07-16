package goroku

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"goroku/goroku/inline"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

var (
	ruRunes       = []rune("ёйцукенгшщзхъфывапролджэячсмитьбю.Ё\"№;%:?ЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭ/ЯЧСМИТЬБЮ,")
	enRunes       = []rune("`qwertyuiop[]asdfghjkl;'zxcvbnm,./~@#$%^&QWERTYUIOP{}ASDFGHJKL:\"|ZXCVBNM<>?")
	cmdNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_\p{L}]+$`)
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
	mu              sync.RWMutex
	modules         *Modules
	client          *CustomTelegramClient
	db              *Database
	rateLimiter     *BoundedRateLimiter
	commands        *BoundedExecutor
	watchers        *BoundedExecutor
	security        *SecurityManager
	me              int64
	cachedUsernames map[string]bool
	stopped         bool
	stopOnce        sync.Once
	taskCancel      context.CancelFunc
}

const (
	defaultCommandCapacity      = 32
	defaultWatcherCapacity      = 64
	defaultRateLimitMaxEntries  = 10_000
	defaultUserRateLimitWindow  = 60 * time.Second
	defaultChatRateLimitWindow  = 200 * time.Second
	defaultUserRateLimitMaximum = 30
	defaultChatRateLimitMaximum = 100
)

// DispatcherConfig provides deterministic construction seams while normal
// clients use the equivalent goroku.dispatcher database settings.
type DispatcherConfig struct {
	RateLimiter     RateLimiterConfig
	CommandCapacity int
	WatcherCapacity int
}

func NewCommandDispatcher(modules *Modules, client *CustomTelegramClient, db *Database) *CommandDispatcher {
	cd, err := NewCommandDispatcherChecked(modules, client, db)
	if err == nil {
		return cd
	}
	L().Error("Command dispatcher initialization failed; using deny-by-default security", zap.Error(err))
	cd, fallbackErr := newCommandDispatcher(modules, client, db, NewSecurityManager(client, db), defaultDispatcherConfig(nil))
	if fallbackErr != nil {
		panic(fallbackErr)
	}
	return cd
}

func NewCommandDispatcherChecked(modules *Modules, client *CustomTelegramClient, db *Database) (*CommandDispatcher, error) {
	security, err := NewSecurityManagerChecked(client, db)
	if err != nil {
		return nil, err
	}
	return newCommandDispatcher(modules, client, db, security, defaultDispatcherConfig(db))
}

// NewCommandDispatcherWithConfig constructs a dispatcher with explicit limits.
func NewCommandDispatcherWithConfig(modules *Modules, client *CustomTelegramClient, db *Database, config DispatcherConfig) (*CommandDispatcher, error) {
	security, err := NewSecurityManagerChecked(client, db)
	if err != nil {
		return nil, err
	}
	return newCommandDispatcher(modules, client, db, security, config)
}

func defaultDispatcherConfig(db *Database) DispatcherConfig {
	config := DispatcherConfig{
		RateLimiter: RateLimiterConfig{
			UserLimit:  defaultUserRateLimitMaximum,
			ChatLimit:  defaultChatRateLimitMaximum,
			UserWindow: defaultUserRateLimitWindow,
			ChatWindow: defaultChatRateLimitWindow,
			MaxEntries: defaultRateLimitMaxEntries,
		},
		CommandCapacity: defaultCommandCapacity,
		WatcherCapacity: defaultWatcherCapacity,
	}
	if db == nil {
		return config
	}
	config.RateLimiter.UserLimit = db.GetInt("goroku.dispatcher", "ratelimit_max_user", config.RateLimiter.UserLimit)
	config.RateLimiter.ChatLimit = db.GetInt("goroku.dispatcher", "ratelimit_max_chat", config.RateLimiter.ChatLimit)
	config.RateLimiter.UserWindow = time.Duration(db.GetInt("goroku.dispatcher", "ratelimit_user_window_seconds", int(config.RateLimiter.UserWindow/time.Second))) * time.Second
	config.RateLimiter.ChatWindow = time.Duration(db.GetInt("goroku.dispatcher", "ratelimit_chat_window_seconds", int(config.RateLimiter.ChatWindow/time.Second))) * time.Second
	config.RateLimiter.MaxEntries = db.GetInt("goroku.dispatcher", "ratelimit_max_entries", config.RateLimiter.MaxEntries)
	config.CommandCapacity = db.GetInt("goroku.dispatcher", "command_capacity", config.CommandCapacity)
	config.WatcherCapacity = db.GetInt("goroku.dispatcher", "watcher_capacity", config.WatcherCapacity)
	return config
}

func newCommandDispatcher(modules *Modules, client *CustomTelegramClient, db *Database, security *SecurityManager, config DispatcherConfig) (*CommandDispatcher, error) {
	limiter, err := NewBoundedRateLimiter(config.RateLimiter)
	if err != nil {
		security.Stop()
		return nil, fmt.Errorf("initialize dispatcher rate limiter: %w", err)
	}
	taskCtx, taskCancel := context.WithCancel(context.Background())
	commands, err := NewBoundedExecutor(BoundedExecutorConfig{Capacity: config.CommandCapacity, Context: taskCtx})
	if err != nil {
		taskCancel()
		security.Stop()
		return nil, fmt.Errorf("initialize command executor: %w", err)
	}
	watchers, err := NewBoundedExecutor(BoundedExecutorConfig{Capacity: config.WatcherCapacity, Context: taskCtx})
	if err != nil {
		taskCancel()
		commands.CloseIntake()
		security.Stop()
		return nil, fmt.Errorf("initialize watcher executor: %w", err)
	}

	cd := &CommandDispatcher{
		modules:         modules,
		client:          client,
		db:              db,
		rateLimiter:     limiter,
		commands:        commands,
		watchers:        watchers,
		me:              client.TGID,
		cachedUsernames: make(map[string]bool),
		security:        security,
		taskCancel:      taskCancel,
	}

	if client.Username != "" {
		cd.cachedUsernames[strings.ToLower(client.Username)] = true
	}
	cd.cachedUsernames[strconv.FormatInt(client.TGID, 10)] = true

	return cd, nil
}

// GetSecurityManager returns the security manager for external use by modules.
func (cd *CommandDispatcher) GetSecurityManager() *SecurityManager {
	return cd.security
}

// GetSecurityManager returns the security manager as inline.SecurityChecker.
func (c *CustomTelegramClient) GetSecurityManager() inline.SecurityChecker {
	if c.Loader == nil {
		return nil
	}
	dispatcher := c.Loader.GetDispatcher()
	if dispatcher == nil {
		return nil
	}
	return dispatcher.GetSecurityManager()
}

func (cd *CommandDispatcher) HandleIncoming(msg *Message) {
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped || msg == nil {
		return
	}

	// Check blacklists
	blacklistChats := cd.getBlacklistChats()
	chatStr := strconv.FormatInt(msg.ChatID, 10)
	if blacklistChats[chatStr] {
		return
	}

	// Check whitelist chats
	whitelistChats := cd.db.GetInt64Slice("goroku.main", "whitelist_chats", nil)

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
	whitelistModules := cd.db.GetStringSlice("goroku.main", "whitelist_modules", nil)

	// Retrieve disabled watchers
	disabledWatchers := cd.db.GetAnyMap("goroku.main", "disabled_watchers", nil)

	// Dispatch message watchers
	for _, watcher := range cd.modules.GetWatchers() {
		if watcher.lease == nil || !watcher.lease.acquire() {
			continue
		}
		release := watcher.lease.release
		modName := watcher.ModuleName

		// Check if this module's watchers are disabled
		if wl, exists := disabledWatchers[modName]; exists {
			if slice, ok := wl.([]any); ok {
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
					release()
					continue
				}
			}
		}

		// Check blacklist chats with specific module (chat_id.module_name)
		key1 := fmt.Sprintf("%s.%s", chatStr, modName)
		key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
		if blacklistChats[key1] || blacklistChats[key2] {
			release()
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
				release()
				continue
			}
		}

		if !cd.watcherTagsMatch(msg, watcher.Meta) {
			release()
			continue
		}

		message := *msg
		err := cd.watchers.Submit(func(ctx context.Context) {
			defer release()
			defer func() {
				if r := recover(); r != nil {
					L().Error("Watcher panic recovered", zap.String("module", modName), zap.Any("panic", r))
				}
			}()
			message.ctx = ctx
			_ = watcher.Handler(&message)
		})
		if err != nil {
			release()
			if errors.Is(err, ErrExecutorCapacity) {
				L().Warn("Watcher dropped", zap.String("module", modName), zap.String("reason", "capacity"))
			}
			if errors.Is(err, ErrExecutorClosed) {
				return
			}
		}
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
	reg, ok := cd.modules.resolveCommand(cmdName)
	if !ok {
		return true
	}
	return cd.handleRegistrationTags(msg, reg)
}

func (cd *CommandDispatcher) handleRegistrationTags(msg *Message, reg *commandRegistration) bool {
	meta := reg.Meta

	if meta.OnlyOwner && !cd.security.IsAccountOwner(msg) {
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
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped || msg == nil || msg.Text == "" {
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
		if msg.Out {
			cleaned := msgText[len(prefix):]
			shouldEdit := false
			if strings.HasPrefix(cleaned, prefix) {
				cmdBody := cleaned[len(prefix):]
				parts := strings.Fields(cmdBody)
				if len(parts) > 0 {
					commandName := parts[0]
					tagParts := strings.Split(commandName, "@")
					actualCmd := tagParts[0]
					if cmdNameRegexp.MatchString(actualCmd) {
						shouldEdit = true
					}
				}
			}
			if shouldEdit {
				// Python: message.edit(message.message[len(prefix):])
				// — edit the same message, stripping one prefix, do NOT send a new message
				_, _ = cd.client.EditMessage(ChatRefID(msg.ChatID), msg.ID, cleaned)
			}
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
		if msg.FwdFrom.FromID != nil {
			switch msg.FwdFrom.FromID.(type) {
			case *tg.PeerChannel:
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
	whitelistChats := cd.db.GetInt64Slice("goroku.main", "whitelist_chats", nil)

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
	reg, releaseRegistration, exists := cd.modules.resolveCommandLease(actualCmd)
	if !exists {
		// Only log debug for owners/whitelisted to avoid spam from other chat members
		if cd.security.Check(msg, "") {
			L().Debug("Command not found in registry", zap.String("cmd", actualCmd), zap.Int64("sender", msg.SenderID))
		}
		return
	}
	defer func() {
		if releaseRegistration != nil {
			releaseRegistration()
		}
	}()
	actualCmd = reg.Name
	handler := reg.Handler
	modName := reg.OwnerName

	// Check blacklist chats with specific module (chat_id.module_name)
	if modName != "" {
		key1 := fmt.Sprintf("%s.%s", chatStr, modName)
		key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
		if blacklistChats[key1] || blacklistChats[key2] {
			return
		}
	}

	// Check whitelist modules (chat_id.module_name)
	whitelistModules := cd.db.GetStringSlice("goroku.main", "whitelist_modules", nil)

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
			noNickname := cd.db.GetBool("goroku.main", "no_nickname", false)

			if !noNickname {
				// Check nonickcmds
				nonickcmds := cd.db.GetStringSlice("goroku.main", "nonickcmds", nil)
				cmdWhitelisted := false
				for _, item := range nonickcmds {
					if strings.EqualFold(item, actualCmd) {
						cmdWhitelisted = true
						break
					}
				}

				// Check nonickusers
				nonickusers := cd.db.GetInt64Slice("goroku.main", "nonickusers", nil)
				userWhitelisted := false
				for _, uid := range nonickusers {
					if uid == msg.SenderID {
						userWhitelisted = true
						break
					}
				}

				// Check nonickchats
				nonickchats := cd.db.GetInt64Slice("goroku.main", "nonickchats", nil)
				chatWhitelisted := false
				for _, cid := range nonickchats {
					if cid == msg.ChatID {
						chatWhitelisted = true
						break
					}
				}

				// Check tsec rules
				tsecWhitelisted := cd.security.checkTsecRegistration(msg.SenderID, reg)

				if !cmdWhitelisted && !userWhitelisted && !chatWhitelisted && !tsecWhitelisted {
					// Nickname checks are enabled, and this command is not whitelisted in any way, so ignore it
					// Only log debug for owners/whitelisted to avoid spam
					if cd.security.Check(msg, "") {
						L().Debug("Nickname check failed, ignoring", zap.String("cmd", actualCmd), zap.Int64("sender", msg.SenderID))
					}
					return
				}
			}
		}
	}

	// Check if the command's module is disabled
	if cd.isRegistrationDisabled(reg) {
		L().Warn("Command or its module is disabled, ignoring", zap.String("cmd", actualCmd))
		return
	}

	// Check security level
	if !cd.security.checkRegistration(msg, reg) {
		L().Debug("Security check failed, ignoring", zap.String("cmd", actualCmd))
		return
	}

	// Check tag filters
	if !cd.handleRegistrationTags(msg, reg) {
		L().Debug("Tag filter failed, ignoring", zap.String("cmd", actualCmd))
		return
	}

	// Claim executor capacity before charging quota. The reservation is released
	// synchronously if a later check rejects the command.
	reservation, err := cd.commands.reserve()
	if err != nil {
		if errors.Is(err, ErrExecutorCapacity) {
			L().Warn("Command rejected", zap.String("cmd", actualCmd), zap.String("reason", "capacity"))
			cd.answerBusy(msg)
		}
		return
	}
	defer reservation.release()
	if !cd.handleRegistrationRatelimit(msg, reg) {
		L().Warn("Rate limit exceeded", zap.String("cmd", actualCmd), zap.Int64("chat", msg.ChatID))
		return
	}

	// Grep pipeline check
	msg = cd.handleGrep(msg)

	// Execute command handler asynchronously when an executor slot is available.
	release := releaseRegistration
	message := *msg
	reservation.start(func(ctx context.Context) {
		defer release()
		defer func() {
			if r := recover(); r != nil {
				L().Error("Command panic recovered", zap.String("cmd", actualCmd), zap.Any("panic", r))
				cd.answerIfPossible(&message, fmt.Sprintf("❌ <b>Command crashed! Panic:</b> <code>%v</code>", r))
				return
			}
		}()
		message.ctx = ctx
		L().Info("Dispatching command", zap.String("cmd", actualCmd))
		originalText := message.Text
		handlerErr := handler(&message)
		if handlerErr != nil {
			L().Error("Command failed", zap.String("cmd", actualCmd), zap.Error(handlerErr))
			cd.answerIfPossible(&message, fmt.Sprintf("❌ <b>Command execution error:</b> <code>%s</code>", handlerErr.Error()))
		} else {
			L().Debug("Command completed", zap.String("cmd", actualCmd), zap.Bool("answered", message.Answered))
			if !message.Answered && message.Text != originalText {
				if message.Client != nil {
					if err := message.Answer(message.Text); err != nil {
						L().Warn("Auto-answer failed", zap.String("cmd", actualCmd), zap.Error(err))
					}
				}
			}
		}
	})
	releaseRegistration = nil
}

func (cd *CommandDispatcher) answerBusy(msg *Message) {
	if msg == nil {
		return
	}
	response := *msg
	response.GrepQuery = ""
	response.GrepInvert = false
	response.CutLines = 0
	response.SplitOutput = false
	cd.answerIfPossible(&response, "⚠️ <b>Busy, try again shortly.</b>")
}

func (cd *CommandDispatcher) answerIfPossible(msg *Message, text string) {
	if msg != nil && msg.Client != nil {
		_ = msg.Answer(text)
	}
}

// Stop prevents new command and watcher handlers from starting.
func (cd *CommandDispatcher) Stop() {
	cd.stopOnce.Do(func() {
		cd.mu.Lock()
		cd.stopped = true
		cd.mu.Unlock()
		cd.taskCancel()
		cd.commands.CloseIntake()
		cd.watchers.CloseIntake()
		if cd.security != nil {
			cd.security.Stop()
		}
	})
}

// Close stops dispatch and waits for handlers that were already running.
func (cd *CommandDispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cd.Stop()
	commandErr := cd.commands.Close(ctx)
	watcherErr := cd.watchers.Close(ctx)
	var securityErr error
	if cd.security != nil {
		securityErr = cd.security.Close(ctx)
	}
	return errors.Join(commandErr, watcherErr, securityErr)
}

func (cd *CommandDispatcher) handleRatelimit(msg *Message, cmdName string) bool {
	reg, _ := cd.modules.resolveCommand(cmdName)
	return cd.handleRegistrationRatelimit(msg, reg)
}

func (cd *CommandDispatcher) handleRegistrationRatelimit(msg *Message, reg *commandRegistration) bool {
	// If owner, bypass rate limits completely
	if cd.security.IsAccountOwner(msg) {
		return true
	}

	weight := 2
	multiplier := 1.0
	if reg != nil && reg.Ratelimited {
		weight = 5
		multiplier = 2.5
	}
	return cd.rateLimiter.Allow(RateLimitEvent{
		UserID:           msg.SenderID,
		ChatID:           msg.ChatID,
		Weight:           weight,
		WindowMultiplier: multiplier,
		Applicable:       true,
	}).Allowed
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
	reg, ok := cd.modules.resolveCommand(cmdName)
	if !ok {
		return false
	}
	return cd.isRegistrationDisabled(reg)
}

func (cd *CommandDispatcher) isRegistrationDisabled(reg *commandRegistration) bool {
	disabledMods := cd.db.GetStringSlice("goroku.main", "disabled_modules", nil)
	modName := reg.OwnerName
	for _, dm := range disabledMods {
		if dm == modName {
			return true
		}
	}
	disabledCmds := cd.db.GetStringMapStringSlice("goroku.main", "disabled_commands", nil)
	if cmds, ok := disabledCmds[modName]; ok {
		for _, dc := range cmds {
			if strings.EqualFold(dc, reg.Name) {
				return true
			}
		}
	}
	return false
}

func (cd *CommandDispatcher) getPrefix(senderID int64) string {
	mainPrefix := cd.db.GetString("goroku.main", "command_prefix", ".")

	if senderID == cd.client.TGID {
		return mainPrefix
	}

	prefixes := cd.db.GetStringMap("goroku.main", "command_prefixes", nil)
	senderStr := strconv.FormatInt(senderID, 10)
	if customPrefix, exists := prefixes[senderStr]; exists && customPrefix != "" {
		return customPrefix
	}

	return mainPrefix
}

func (cd *CommandDispatcher) getBlacklistChats() map[string]bool {
	res := make(map[string]bool)
	chats := cd.db.GetStringSlice("goroku.main", "blacklist_chats", nil)
	for _, item := range chats {
		res[item] = true
	}
	return res
}

// HandleInlineQuery handles incoming MTProto inline queries for the bot
func (cd *CommandDispatcher) HandleInlineQuery(query *tg.UpdateBotInlineQuery) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.stopped {
		return
	}
	L().Debug("Received inline query", zap.String("query", query.Query), zap.Int64("user", query.UserID))
	// Placeholder for routing to inline handlers
}

// HandleCallbackQuery handles incoming MTProto callback queries for the bot
func (cd *CommandDispatcher) HandleCallbackQuery(query *tg.UpdateBotCallbackQuery) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.stopped {
		return
	}
	L().Debug("Received callback query", zap.String("data", string(query.Data)), zap.Int64("user", query.UserID))
	// Placeholder for routing to callback handlers
}
