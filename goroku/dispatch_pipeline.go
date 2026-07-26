package goroku

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// DispatchReason is a stable reject/allow code for debug and audit logs.
type DispatchReason string

const (
	ReasonOK              DispatchReason = "ok"
	ReasonStopped         DispatchReason = "stopped"
	ReasonNilMessage      DispatchReason = "nil_message"
	ReasonEmptyText       DispatchReason = "empty_text"
	ReasonNoPrefix        DispatchReason = "no_prefix"
	ReasonDoublePrefix    DispatchReason = "double_prefix"
	ReasonSkipMedia       DispatchReason = "skip_media"
	ReasonBlacklistChat   DispatchReason = "blacklist_chat"
	ReasonWhitelistChat   DispatchReason = "whitelist_chat"
	ReasonBlacklistModule DispatchReason = "blacklist_module"
	ReasonWhitelistModule DispatchReason = "whitelist_module"
	ReasonDisabledWatcher DispatchReason = "disabled_watcher"
	ReasonDisabledModule  DispatchReason = "disabled_module"
	ReasonCommandNotFound DispatchReason = "command_not_found"
	ReasonTargetMismatch  DispatchReason = "target_mismatch"
	ReasonNickname        DispatchReason = "nickname"
	ReasonSecurity        DispatchReason = "security"
	ReasonOnlyOwner       DispatchReason = "only_owner"
	ReasonOnlyPM          DispatchReason = "only_pm"
	ReasonNoPM            DispatchReason = "no_pm"
	ReasonOnlyChats       DispatchReason = "only_chats"
	ReasonOnlyGroups      DispatchReason = "only_groups"
	ReasonOnlyChannels    DispatchReason = "only_channels"
	ReasonNoForwarded     DispatchReason = "no_forwarded"
	ReasonNoReply         DispatchReason = "no_reply"
	ReasonOnlyReply       DispatchReason = "only_reply"
	ReasonNoCommands      DispatchReason = "no_commands"
	ReasonOnlyCommands    DispatchReason = "only_commands"
	ReasonFromID          DispatchReason = "from_id"
	ReasonChatID          DispatchReason = "chat_id"
	ReasonRegex           DispatchReason = "regex"
	ReasonStartsWith      DispatchReason = "starts_with"
	ReasonEndsWith        DispatchReason = "ends_with"
	ReasonContains        DispatchReason = "contains"
	ReasonFilter          DispatchReason = "filter"
	ReasonRateLimit       DispatchReason = "rate_limit"
	ReasonCapacity        DispatchReason = "capacity"
	ReasonExecutorClosed  DispatchReason = "executor_closed"
	ReasonLease           DispatchReason = "lease"
)

// Allowed reports whether the reason permits dispatch.
func (r DispatchReason) Allowed() bool {
	return r == ReasonOK
}

// metaFilterOptions selects which predicate groups apply for a given stage.
type metaFilterOptions struct {
	applyCommandOnly bool
	isCommand        bool
	applyOwner       bool
	isOwner          bool
	applyReply       bool
	regex            *regexp.Regexp
}

// matchMetadata is the shared command/watcher metadata predicate stage.
func matchMetadata(msg *Message, meta CommandMeta, opts metaFilterOptions) DispatchReason {
	if msg == nil {
		return ReasonNilMessage
	}
	if opts.applyCommandOnly {
		if meta.NoCommands && opts.isCommand {
			return ReasonNoCommands
		}
		if meta.OnlyCommands && !opts.isCommand {
			return ReasonOnlyCommands
		}
	}
	if opts.applyOwner && meta.OnlyOwner && !opts.isOwner {
		return ReasonOnlyOwner
	}
	if meta.OnlyPM && !msg.IsPrivate {
		return ReasonOnlyPM
	}
	if meta.NoPM && msg.IsPrivate {
		return ReasonNoPM
	}
	if meta.OnlyChats && msg.IsPrivate {
		return ReasonOnlyChats
	}
	if meta.OnlyGroups && !msg.IsGroup {
		return ReasonOnlyGroups
	}
	if meta.OnlyChannels && !msg.IsChannel {
		return ReasonOnlyChannels
	}
	if meta.NoForwarded && msg.IsForwarded {
		return ReasonNoForwarded
	}
	if opts.applyReply {
		if meta.NoReply && msg.ReplyToMsgID != 0 {
			return ReasonNoReply
		}
		if meta.OnlyReply && msg.ReplyToMsgID == 0 {
			return ReasonOnlyReply
		}
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
			return ReasonFromID
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
			return ReasonChatID
		}
	}
	if meta.Regex != "" {
		re := opts.regex
		if re == nil {
			var err error
			re, err = regexp.Compile(meta.Regex)
			if err != nil {
				return ReasonRegex
			}
		}
		if !re.MatchString(msg.RawText) {
			return ReasonRegex
		}
	}
	if meta.StartsWith != "" && !strings.HasPrefix(msg.RawText, meta.StartsWith) {
		return ReasonStartsWith
	}
	if meta.EndsWith != "" && !strings.HasSuffix(msg.RawText, meta.EndsWith) {
		return ReasonEndsWith
	}
	if meta.Contains != "" && !strings.Contains(msg.RawText, meta.Contains) {
		return ReasonContains
	}
	if meta.Filter != nil && !meta.Filter(msg) {
		return ReasonFilter
	}
	return ReasonOK
}

// compileMetaRegex compiles CommandMeta.Regex once at registration time.
// Empty patterns yield a nil compiled value.
func compileMetaRegex(meta CommandMeta, subject string) (*regexp.Regexp, error) {
	if meta.Regex == "" {
		return nil, nil
	}
	re, err := regexp.Compile(meta.Regex)
	if err != nil {
		return nil, fmt.Errorf("invalid regex for %s: %w", subject, err)
	}
	return re, nil
}

// chatPolicy is the global chat blacklist/whitelist stage.
func (cd *CommandDispatcher) chatPolicy(msg *Message) (DispatchReason, map[string]bool, string) {
	blacklistChats := cd.getBlacklistChats()
	chatStr := strconv.FormatInt(msg.ChatID, 10)
	if blacklistChats[chatStr] {
		return ReasonBlacklistChat, blacklistChats, chatStr
	}
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
			return ReasonWhitelistChat, blacklistChats, chatStr
		}
	}
	return ReasonOK, blacklistChats, chatStr
}

// moduleChatPolicy is the per-module chat blacklist/whitelist stage.
func (cd *CommandDispatcher) moduleChatPolicy(chatStr, modName string, blacklistChats map[string]bool, whitelistModules []string) DispatchReason {
	if modName == "" {
		return ReasonOK
	}
	key1 := fmt.Sprintf("%s.%s", chatStr, modName)
	key2 := fmt.Sprintf("%s.%s", chatStr, strings.ToLower(modName))
	if blacklistChats[key1] || blacklistChats[key2] {
		return ReasonBlacklistModule
	}
	if len(whitelistModules) > 0 {
		found := false
		for _, wm := range whitelistModules {
			if wm == key1 || wm == key2 {
				found = true
				break
			}
		}
		if !found {
			return ReasonWhitelistModule
		}
	}
	return ReasonOK
}

// watcherDisabledPolicy checks disabled_watchers entries for a module.
func (cd *CommandDispatcher) watcherDisabledPolicy(msg *Message, modName, chatStr string, disabledWatchers map[string]any) DispatchReason {
	if disabledWatchers == nil {
		return ReasonOK
	}
	wl, exists := disabledWatchers[modName]
	if !exists {
		return ReasonOK
	}
	slice, ok := wl.([]any)
	if !ok {
		return ReasonOK
	}
	for _, item := range slice {
		valStr := fmt.Sprintf("%v", item)
		switch {
		case valStr == "*":
			return ReasonDisabledWatcher
		case valStr == chatStr:
			return ReasonDisabledWatcher
		case valStr == "only_chats" && msg.IsPrivate:
			return ReasonDisabledWatcher
		case valStr == "only_pm" && !msg.IsPrivate:
			return ReasonDisabledWatcher
		case valStr == "out" && !msg.Out:
			return ReasonDisabledWatcher
		case valStr == "in" && msg.Out:
			return ReasonDisabledWatcher
		}
	}
	return ReasonOK
}

// nicknamePolicy enforces mention / nonick* exceptions for inbound group commands.
func (cd *CommandDispatcher) nicknamePolicy(msg *Message, reg *commandRegistration, actualCmd string) DispatchReason {
	if msg.Out || msg.IsPrivate {
		return ReasonOK
	}
	mentioned := false
	if username := cd.client.Username(); username != "" && strings.Contains(strings.ToLower(msg.Text), "@"+strings.ToLower(username)) {
		mentioned = true
	}
	if mentioned {
		return ReasonOK
	}
	if cd.db.GetBool("goroku.main", "no_nickname", false) {
		return ReasonOK
	}

	nonickcmds := cd.db.GetStringSlice("goroku.main", "nonickcmds", nil)
	for _, item := range nonickcmds {
		if strings.EqualFold(item, actualCmd) {
			return ReasonOK
		}
	}
	nonickusers := cd.db.GetInt64Slice("goroku.main", "nonickusers", nil)
	for _, uid := range nonickusers {
		if uid == msg.SenderID {
			return ReasonOK
		}
	}
	nonickchats := cd.db.GetInt64Slice("goroku.main", "nonickchats", nil)
	for _, cid := range nonickchats {
		if cid == msg.ChatID {
			return ReasonOK
		}
	}
	if cd.security.checkTsecRegistration(msg.SenderID, reg) {
		return ReasonOK
	}
	return ReasonNickname
}

func (cd *CommandDispatcher) logReject(stage string, reason DispatchReason, fields ...zap.Field) {
	if reason.Allowed() {
		return
	}
	base := []zap.Field{
		zap.String("stage", stage),
		zap.String("reason", string(reason)),
	}
	L().Debug("Dispatch rejected", append(base, fields...)...)
}
