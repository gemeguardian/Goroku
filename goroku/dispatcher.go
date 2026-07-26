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
	grepRegexp    = regexp.MustCompile(`\| ?grep (.+)`)
	cutRegexp     = regexp.MustCompile(`\| ?cut (\d+)`)
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
	outgoing        *BoundedExecutor
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
	// defaultOutgoingCapacity bounds Telegram calls moved off the update loop.
	// These are courtesy messages (the "busy" reply, the double-prefix edit),
	// so a small pool is enough; excess ones are dropped rather than queued.
	defaultOutgoingCapacity = 8
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
	// Separate from commands on purpose: the calls sent here include the
	// "busy" reply, which is produced exactly when the command executor is
	// full. Sharing that pool would drop the reply whenever it is needed.
	outgoing, err := NewBoundedExecutor(BoundedExecutorConfig{Capacity: defaultOutgoingCapacity, Context: taskCtx})
	if err != nil {
		taskCancel()
		commands.CloseIntake()
		watchers.CloseIntake()
		security.Stop()
		return nil, fmt.Errorf("initialize outgoing executor: %w", err)
	}

	cd := &CommandDispatcher{
		modules:         modules,
		client:          client,
		db:              db,
		rateLimiter:     limiter,
		commands:        commands,
		watchers:        watchers,
		outgoing:        outgoing,
		me:              client.TGIDValue(),
		cachedUsernames: make(map[string]bool),
		security:        security,
		taskCancel:      taskCancel,
	}

	if username := client.Username(); username != "" {
		cd.cachedUsernames[strings.ToLower(username)] = true
	}
	cd.cachedUsernames[strconv.FormatInt(client.TGIDValue(), 10)] = true

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
	// Pipeline: ChatPolicy -> RegistryLookup(watchers) -> disabled/module policy
	//           -> MetadataFilter -> Executor
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped || msg == nil {
		return
	}

	reason, blacklistChats, chatStr := cd.chatPolicy(msg)
	if !reason.Allowed() {
		cd.logReject("chat_policy", reason, zap.Int64("chat", msg.ChatID))
		return
	}

	whitelistModules := cd.db.GetStringSlice("goroku.main", "whitelist_modules", nil)
	disabledWatchers := cd.db.GetAnyMap("goroku.main", "disabled_watchers", nil)

	for _, watcher := range cd.modules.GetWatchers() {
		if watcher.lease == nil || !watcher.lease.acquire() {
			continue
		}
		release := watcher.lease.release
		modName := watcher.ModuleName

		// Routine watcher rejects stay silent; reason codes are available for
		// callers/tests and capacity/closed paths still warn.
		if !cd.watcherDisabledPolicy(msg, modName, chatStr, disabledWatchers).Allowed() {
			release()
			continue
		}
		if !cd.moduleChatPolicy(chatStr, modName, blacklistChats, whitelistModules).Allowed() {
			release()
			continue
		}
		if !cd.watcherMetadataFilter(msg, watcher).Allowed() {
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
				L().Warn("Watcher dropped", zap.String("module", modName), zap.String("reason", string(ReasonCapacity)))
			}
			if errors.Is(err, ErrExecutorClosed) {
				return
			}
		}
	}
}

func (cd *CommandDispatcher) watcherTagsMatch(msg *Message, meta CommandMeta) bool {
	return cd.watcherMetadataFilter(msg, RegisteredWatcher{Meta: meta}).Allowed()
}

func (cd *CommandDispatcher) watcherMetadataFilter(msg *Message, watcher RegisteredWatcher) DispatchReason {
	return matchMetadata(msg, watcher.Meta, metaFilterOptions{
		applyCommandOnly: true,
		isCommand:        cd.isCommandMessage(msg),
		regex:            watcher.regex,
	})
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
	return cd.commandMetadataFilter(msg, reg).Allowed()
}

func (cd *CommandDispatcher) commandMetadataFilter(msg *Message, reg *commandRegistration) DispatchReason {
	if reg == nil {
		return ReasonOK
	}
	return matchMetadata(msg, reg.Meta, metaFilterOptions{
		applyOwner: true,
		isOwner:    cd.security.IsAccountOwner(msg),
		applyReply: true,
		regex:      reg.regex,
	})
}

func (cd *CommandDispatcher) HandleCommand(msg *Message) {
	// Pipeline: Parser -> RegistryLookup -> ChatPolicy -> SecurityPolicy
	//           -> MetadataFilter -> RateLimiter -> Executor
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped || msg == nil || msg.Text == "" {
		return
	}

	parsed, reason := cd.parseCommand(msg)
	if !reason.Allowed() {
		return
	}

	if msg.Media != nil {
		switch msg.Media.(type) {
		case *tg.MessageMediaDice, *tg.MessageMediaGame:
			return
		}
	}

	reason, blacklistChats, chatStr := cd.chatPolicy(msg)
	if !reason.Allowed() {
		cd.logReject("chat_policy", reason, zap.Int64("chat", msg.ChatID))
		return
	}

	reg, releaseRegistration, exists := cd.modules.resolveCommandLease(parsed.actualCmd)
	if !exists {
		if cd.security.Check(msg, "") {
			cd.logReject("registry_lookup", ReasonCommandNotFound, zap.String("cmd", parsed.actualCmd), zap.Int64("sender", msg.SenderID))
		}
		return
	}
	defer func() {
		if releaseRegistration != nil {
			releaseRegistration()
		}
	}()
	actualCmd := reg.Name
	handler := reg.Handler
	modName := reg.OwnerName

	whitelistModules := cd.db.GetStringSlice("goroku.main", "whitelist_modules", nil)
	if reason := cd.moduleChatPolicy(chatStr, modName, blacklistChats, whitelistModules); !reason.Allowed() {
		cd.logReject("module_chat_policy", reason, zap.String("cmd", actualCmd), zap.String("module", modName))
		return
	}

	if reason := cd.nicknamePolicy(msg, reg, actualCmd); !reason.Allowed() {
		if cd.security.Check(msg, "") {
			cd.logReject("security_policy", reason, zap.String("cmd", actualCmd), zap.Int64("sender", msg.SenderID))
		}
		return
	}

	if cd.isRegistrationDisabled(reg) {
		L().Warn("Command or its module is disabled, ignoring", zap.String("cmd", actualCmd), zap.String("reason", string(ReasonDisabledModule)))
		return
	}

	if !cd.security.checkRegistration(msg, reg) {
		cd.logReject("security_policy", ReasonSecurity, zap.String("cmd", actualCmd))
		return
	}

	if reason := cd.commandMetadataFilter(msg, reg); !reason.Allowed() {
		cd.logReject("metadata_filter", reason, zap.String("cmd", actualCmd))
		return
	}

	// Claim executor capacity before charging quota. The reservation is released
	// synchronously if a later check rejects the command.
	reservation, err := cd.commands.reserve()
	if err != nil {
		if errors.Is(err, ErrExecutorCapacity) {
			L().Warn("Command rejected", zap.String("cmd", actualCmd), zap.String("reason", string(ReasonCapacity)))
			cd.answerBusy(msg)
		}
		return
	}
	defer reservation.release()
	if !cd.handleRegistrationRatelimit(msg, reg) {
		L().Warn("Rate limit exceeded", zap.String("cmd", actualCmd), zap.String("reason", string(ReasonRateLimit)), zap.Int64("chat", msg.ChatID))
		return
	}

	msg = cd.handleGrep(msg)

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
						if !isMessageNotModifiedError(err) {
							L().Warn("Auto-answer failed", zap.String("cmd", actualCmd), zap.Error(err))
						}
					}
				}
			}
		}
	})
	releaseRegistration = nil
}

func isMessageNotModifiedError(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "MESSAGE_NOT_MODIFIED")
}

type parsedCommand struct {
	actualCmd string
}

// parseCommand is the Parser stage: prefix, layout correction, target, and cmd name.
func (cd *CommandDispatcher) parseCommand(msg *Message) (parsedCommand, DispatchReason) {
	prefix := cd.getPrefix(msg.SenderID)
	translatedPrefix := translateLayout(prefix)
	msgText := msg.RawText
	if msgText == "" {
		msgText = msg.Text
	}

	if strings.HasPrefix(msgText, translatedPrefix) && translatedPrefix != prefix {
		msgText = translateLayout(msgText)
	}

	if !strings.HasPrefix(msgText, prefix) {
		return parsedCommand{}, ReasonNoPrefix
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
				// Off the update-reading goroutine: this is a network round
				// trip, and gotd delivers updates serially, so doing it here
				// delayed every subsequent update behind it.
				cd.submitOutgoing(func(ctx context.Context) {
					_, _ = cd.client.EditMessageContext(ctx, ChatRefID(msg.ChatID), msg.ID, cleaned)
				})
			}
		}
		return parsedCommand{}, ReasonDoublePrefix
	}

	cmdBody := strings.TrimLeft(msgText[len(prefix):], " \t\r\n")
	parts := strings.Fields(cmdBody)
	if len(parts) == 0 {
		return parsedCommand{}, ReasonEmptyText
	}
	// Keep command helpers from treating a space after the prefix as an argument.
	// RawText must remain plain Telegram text; Text may contain escaped HTML entities.
	msg.Text = prefix + cmdBody
	msg.RawText = prefix + cmdBody

	commandName := parts[0]
	tagParts := strings.Split(commandName, "@")

	if len(tagParts) == 2 {
		target := strings.ToLower(tagParts[1])
		if target == "me" {
			if !msg.Out {
				return parsedCommand{}, ReasonTargetMismatch
			}
		} else if !cd.cachedUsernames[target] {
			return parsedCommand{}, ReasonTargetMismatch
		}
	}

	return parsedCommand{actualCmd: tagParts[0]}, ReasonOK
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
	// Also a network round trip on the update path: the "busy" reply must not
	// itself hold up the updates queued behind it.
	cd.submitOutgoing(func(ctx context.Context) {
		reply := response
		reply.ctx = ctx
		cd.answerIfPossible(&reply, "⚠️ <b>Busy, try again shortly.</b>")
	})
}

// submitOutgoing runs an outgoing Telegram call off the update-reading
// goroutine. gotd delivers updates serially through one handler, so any RPC
// made inline there delays every update behind it. If the executor is full or
// closed the call is dropped: these are courtesy replies, not the command
// itself, and blocking the update loop to deliver one is the worse outcome.
func (cd *CommandDispatcher) submitOutgoing(task func(context.Context)) {
	if err := cd.outgoing.Submit(task); err != nil {
		L().Debug("Dropped outgoing dispatcher call", zap.Error(err))
	}
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
		cd.outgoing.CloseIntake()
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
	outgoingErr := cd.outgoing.Close(ctx)
	var securityErr error
	if cd.security != nil {
		securityErr = cd.security.Close(ctx)
	}
	return errors.Join(commandErr, watcherErr, outgoingErr, securityErr)
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
	loc := grepRegexp.FindStringSubmatch(msg.RawText)
	if len(loc) == 2 {
		query := strings.TrimSpace(loc[1])
		invert := false
		if strings.HasPrefix(query, "-v ") {
			invert = true
			query = strings.TrimSpace(query[3:])
		}

		cleaned := grepRegexp.ReplaceAllString(msg.Text, "")
		msg.Text = cleaned
		msg.RawText = cleaned

		msg.GrepQuery = query
		msg.GrepInvert = invert
	}

	if loc := cutRegexp.FindStringSubmatch(msg.RawText); len(loc) == 2 {
		n, _ := strconv.Atoi(loc[1])
		msg.CutLines = n
		msg.Text = cutRegexp.ReplaceAllString(msg.Text, "")
		msg.RawText = cutRegexp.ReplaceAllString(msg.RawText, "")
	}

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

	if senderID == cd.client.TGIDValue() {
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

// HandleInlineQuery acknowledges MTProto bot inline updates.
// Production inline routing uses the Bot API InlineManager; MTProto bot
// sessions are not the supported path, so these updates are intentionally ignored.
func (cd *CommandDispatcher) HandleInlineQuery(query *tg.UpdateBotInlineQuery) {
	if query == nil {
		return
	}
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped {
		return
	}
	L().Debug("MTProto inline query ignored",
		zap.String("reason", "mtproto_inline_unsupported"),
		zap.String("query", query.Query),
		zap.Int64("user", query.UserID),
	)
}

// HandleCallbackQuery acknowledges MTProto bot callback updates.
// Production callbacks use the Bot API InlineManager.
func (cd *CommandDispatcher) HandleCallbackQuery(query *tg.UpdateBotCallbackQuery) {
	if query == nil {
		return
	}
	cd.mu.RLock()
	stopped := cd.stopped
	cd.mu.RUnlock()
	if stopped {
		return
	}
	L().Debug("MTProto callback query ignored",
		zap.String("reason", "mtproto_callback_unsupported"),
		zap.String("data", string(query.Data)),
		zap.Int64("user", query.UserID),
	)
}
