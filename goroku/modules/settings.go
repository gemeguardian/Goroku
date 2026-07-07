package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"sort"
	"strconv"
	"strings"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
)

type SettingsModule struct {
	client                   *goroku.CustomTelegramClient
	db                       *goroku.Database
	translator               *goroku.Translator
	allowNonstandardPrefixes bool
	aliasEmoji               string
}

func (m *SettingsModule) Name() string {
	return "Settings"
}

func (m *SettingsModule) Strings() map[string]string {
	return map[string]string{
		"name":                            "Settings",
		"_cfg_allow_nonstandart_prefixes": "Allow non-standard command prefixes (like flags or multiple characters)",
		"_cfg_alias_emoji":                "Emoji/tag for alias list bullet",
	}
}

func (m *SettingsModule) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *SettingsModule) ClientReady() error {
	// Load aliases from database on startup
	if loader := m.client.Loader; loader != nil {
		for alias, target := range m.db.GetStringMap("Settings", "aliases", nil) {
			parts := strings.Fields(target)
			if len(parts) > 0 {
				loader.AddAlias(alias, parts[0])
			}
		}
	}
	return nil
}

func (m *SettingsModule) OnUnload() error { return nil }
func (m *SettingsModule) OnDlmod() error  { return nil }

func (m *SettingsModule) ConfigDefaults() map[string]any {
	return map[string]any{
		"allow_nonstandart_prefixes": false,
		"alias_emoji":                "<tg-emoji emoji-id=4974259868996207180>▪️</tg-emoji>",
	}
}

func (m *SettingsModule) ConfigReady(config map[string]any) error {
	if val, ok := config["allow_nonstandart_prefixes"].(bool); ok {
		m.allowNonstandardPrefixes = val
	}
	if val, ok := config["alias_emoji"].(string); ok {
		m.aliasEmoji = val
	}
	return nil
}

func (m *SettingsModule) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"goroku":          m.GorokuCmd,
		"blacklist":       m.BlacklistCmd,
		"unblacklist":     m.UnblacklistCmd,
		"blacklistuser":   m.BlacklistUserCmd,
		"unblacklistuser": m.UnblacklistUserCmd,
		"setprefix":       m.SetPrefixCmd,
		"aliases":         m.AliasesCmd,
		"addalias":        m.AddAliasCmd,
		"delalias":        m.DelAliasCmd,
		"cleardb":         m.ClearDBCmd,
		"togglecmd":       m.ToggleCmdCmd,
		"togglemod":       m.ToggleModCmd,
		"clearmodule":     m.ClearModuleCmd,
		"installation":    m.InstallationCmd,
	}
}

func (m *SettingsModule) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *SettingsModule) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *SettingsModule) blacklistCommon(msg *goroku.Message) (string, bool, error) {
	args := utils.GetArgs(msg.Text)
	if len(args) > 2 {
		_ = msg.Answer(m.getTrans("too_many_args", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Too many args</b>"))
		return "", false, nil
	}

	var chatID int64
	var module string
	hasChatID := false

	if len(args) > 0 {
		if val, err := strconv.ParseInt(args[0], 10, 64); err == nil {
			chatID = val
			hasChatID = true
		} else {
			module = args[0]
		}
	}

	if len(args) == 2 {
		module = args[1]
	}

	if !hasChatID {
		chatID = msg.ChatID
	}

	if module != "" {
		if loader := m.client.Loader; loader != nil {
			if mod := loader.LookupByName(module); mod != nil {
				module = mod.Name()
			}
		}
		return fmt.Sprintf("%d.%s", chatID, module), true, nil
	}

	return strconv.FormatInt(chatID, 10), true, nil
}

func (m *SettingsModule) BlacklistCmd(msg *goroku.Message) error {
	res, ok, _ := m.blacklistCommon(msg)
	if !ok {
		return nil
	}
	if strings.HasPrefix(res, "-100") {
		res = res[4:]
	}

	chats := m.db.GetStringSlice("goroku.main", "blacklist_chats", nil)

	found := false
	for _, c := range chats {
		if c == res {
			found = true
			break
		}
	}
	if !found {
		chats = append(chats, res)
		m.db.SetStringSlice("goroku.main", "blacklist_chats", chats)
	}

	text := formatTrans(m.getTrans("blacklisted", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Chat {} blacklisted from userbot</b>"), res)
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) UnblacklistCmd(msg *goroku.Message) error {
	res, ok, _ := m.blacklistCommon(msg)
	if !ok {
		return nil
	}
	if strings.HasPrefix(res, "-100") {
		res = res[4:]
	}

	chats := m.db.GetStringSlice("goroku.main", "blacklist_chats", nil)

	var newChats []string
	for _, c := range chats {
		if c != res {
			newChats = append(newChats, c)
		}
	}
	m.db.SetStringSlice("goroku.main", "blacklist_chats", newChats)

	text := formatTrans(m.getTrans("unblacklisted", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Chat {} unblacklisted from userbot</b>"), res)
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) getUser(msg *goroku.Message) (int64, bool) {
	args := utils.GetArgs(msg.Text)
	if len(args) > 0 {
		if id, err := strconv.ParseInt(args[0], 10, 64); err == nil {
			return id, true
		}
	}
	if msg.ReplyToMsgID != 0 {
		replyMsg, err := msg.GetReplyMessage()
		if err == nil && replyMsg != nil {
			return replyMsg.SenderID, true
		}
	}
	if msg.IsPrivate {
		return msg.ChatID, true
	}
	return 0, false
}

func (m *SettingsModule) BlacklistUserCmd(msg *goroku.Message) error {
	user, ok := m.getUser(msg)
	if !ok {
		_ = msg.Answer(m.getTrans("who_to_blacklist", "<tg-emoji emoji-id=5382187118216879236>❓</tg-emoji> <b>Who to blacklist?</b>"))
		return nil
	}

	users := m.db.GetInt64Slice("goroku.main", "blacklist_users", nil)

	found := false
	for _, u := range users {
		if u == user {
			found = true
			break
		}
	}
	if !found {
		users = append(users, user)
		m.db.SetInt64Slice("goroku.main", "blacklist_users", users)
	}

	text := formatTrans(m.getTrans("user_blacklisted", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>User {} blacklisted from userbot</b>"), strconv.FormatInt(user, 10))
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) UnblacklistUserCmd(msg *goroku.Message) error {
	user, ok := m.getUser(msg)
	if !ok {
		_ = msg.Answer(m.getTrans("who_to_unblacklist", "<tg-emoji emoji-id=5382187118216879236>❓</tg-emoji> <b>Who to unblacklist?</b>"))
		return nil
	}

	users := m.db.GetInt64Slice("goroku.main", "blacklist_users", nil)

	var newUsers []int64
	for _, u := range users {
		if u != user {
			newUsers = append(newUsers, u)
		}
	}
	m.db.SetInt64Slice("goroku.main", "blacklist_users", newUsers)

	text := formatTrans(m.getTrans("user_unblacklisted", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>User {} unblacklisted from userbot</b>"), strconv.FormatInt(user, 10))
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) isUserInSecurity(userID int64) bool {
	if userID == m.client.TGID {
		return true
	}

	ownerVal := m.db.GetInt64Slice("goroku.security", "owner", nil)
	for _, id := range ownerVal {
		if id == userID {
			return true
		}
	}

	tsecVal, _ := m.db.Get("goroku.security", "tsec_user", []any{})
	if slice, ok := tsecVal.([]any); ok {
		for _, v := range slice {
			if rMap, ok := v.(map[string]any); ok {
				if targetVal, ok := rMap["target"]; ok {
					var id int64
					switch val := targetVal.(type) {
					case float64:
						id = int64(val)
					case int64:
						id = val
					}
					if id == userID {
						return true
					}
				}
			}
		}
	}

	sgroupsVal, _ := m.db.Get("goroku.security", "security_groups", map[string]any{})
	if sgroupsMap, ok := sgroupsVal.(map[string]any); ok {
		for _, sgVal := range sgroupsMap {
			if sgMap, ok := sgVal.(map[string]any); ok {
				if usersVal, ok := sgMap["users"]; ok {
					if usersSlice, ok := usersVal.([]any); ok {
						for _, uVal := range usersSlice {
							var id int64
							switch val := uVal.(type) {
							case float64:
								id = int64(val)
							case int64:
								id = val
							}
							if id == userID {
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

func (m *SettingsModule) getPrefix(userID int64) string {
	if userID != 0 {
		prefixes := m.db.GetStringMap("goroku.main", "command_prefixes", nil)
		if p, exists := prefixes[strconv.FormatInt(userID, 10)]; exists {
			return p
		}
	}
	return m.db.GetString("goroku.main", "command_prefix", ".")
}

func (m *SettingsModule) SetPrefixCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) == 0 {
		_ = msg.Answer(m.getTrans("what_prefix", "<tg-emoji emoji-id=5382187118216879236>❓</tg-emoji> <b>What should the prefix be set to?</b>"))
		return nil
	}

	newPrefix := args[0]
	runes := []rune(newPrefix)
	if len(runes) != 1 && !m.allowNonstandardPrefixes {
		_ = msg.Answer(m.getTrans("prefix_incorrect", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Prefix must be one symbol in length</b>"))
		return nil
	}

	if newPrefix == "s" {
		_ = msg.Answer(m.getTrans("prefix_incorrect", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Prefix must be one symbol in length</b>"))
		return nil
	}

	if len(args) == 2 {
		var userID int64
		var userName string

		entity, err := m.client.GetEntity(args[1], 0, false)
		if err != nil {
			_ = msg.Answer(m.getTrans("invalid_id_or_username", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> Invalid id/username was given"))
			return nil
		}

		if u, ok := entity.(*tg.User); ok {
			userID = u.ID
			userName = u.FirstName
		} else {
			entityType := strings.TrimPrefix(fmt.Sprintf("%T", entity), "*")
			if !strings.Contains(strings.ToLower(entityType), "user") {
				_ = msg.Answer(fmt.Sprintf("The entity %s is not a User", args[1]))
				return nil
			}
			userID = utils.GetEntityID(entity)
			userName = args[1]
		}

		if userID != m.client.TGID {
			if !m.isUserInSecurity(userID) {
				_ = msg.Answer(m.getTrans("id_not_found_scgroup", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> This entity does not exist in any security group. Therefore, adding a prefix for it is pointless"))
				return nil
			}

			oldPrefix := utils.EscapeHTML(m.getPrefix(userID))
			allPrefixes := m.db.GetStringMap("goroku.main", "command_prefixes", nil)
			allPrefixes[strconv.FormatInt(userID, 10)] = newPrefix
			m.db.SetStringMap("goroku.main", "command_prefixes", allPrefixes)

			text := m.getTrans("entity_prefix_set", "{} <b>Command prefix updated for {entity_name}. Use the following command to change it back:</b>\n<pre><code class=\"language-goroku\">{newprefix}setprefix {oldprefix} {entity_id}</code></pre>")
			text = strings.Replace(text, "{}", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji>", 1)
			text = strings.ReplaceAll(text, "{entity_name}", utils.EscapeHTML(userName))
			text = strings.ReplaceAll(text, "{newprefix}", utils.EscapeHTML(newPrefix))
			text = strings.ReplaceAll(text, "{oldprefix}", utils.EscapeHTML(oldPrefix))
			text = strings.ReplaceAll(text, "{entity_id}", strconv.FormatInt(userID, 10))

			_ = msg.Answer(text)
			return nil
		}
	}

	oldPrefix := utils.EscapeHTML(m.getPrefix(0))
	m.db.Set("goroku.main", "command_prefix", newPrefix)

	text := m.getTrans("prefix_set", "{} <b>Command prefix updated. Use the following command to change it back:</b>\n<pre><code class=\"language-goroku\">{newprefix}setprefix {oldprefix}</code></pre>")
	text = strings.Replace(text, "{}", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji>", 1)
	text = strings.ReplaceAll(text, "{newprefix}", utils.EscapeHTML(newPrefix))
	text = strings.ReplaceAll(text, "{oldprefix}", utils.EscapeHTML(oldPrefix))

	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) AliasesCmd(msg *goroku.Message) error {
	loader := msg.Client.Loader
	if loader == nil {
		return nil
	}

	aliases := loader.GetAliases()
	var aliasKeys []string
	for alias := range aliases {
		aliasKeys = append(aliasKeys, alias)
	}
	sort.Strings(aliasKeys)

	var listLines []string
	for _, alias := range aliasKeys {
		listLines = append(listLines, fmt.Sprintf("%s <code>%s</code> &lt;- %s", m.aliasEmoji, alias, aliases[alias]))
	}

	text := m.getTrans("aliases", "<b>🔗 Aliases:</b>\n") + "<blockquote expandable>" + strings.Join(listLines, "\n") + "</blockquote>"
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) AddAliasCmd(msg *goroku.Message) error {
	raw := strings.TrimSpace(utils.GetArgsRaw(msg.Text))
	parts := strings.Fields(raw)
	if len(parts) < 2 {
		_ = msg.Answer(m.getTrans("alias_args", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>You must provide a command and the alias for it</b>"))
		return nil
	}

	alias := parts[0]
	cmd := parts[1]

	loader := msg.Client.Loader
	if loader == nil {
		return nil
	}

	if loader.AddAlias(alias, cmd) {
		aliases := m.db.GetStringMap("Settings", "aliases", nil)
		var target string
		if len(parts) > 2 {
			target = cmd + " " + strings.Join(parts[2:], " ")
		} else {
			target = cmd
		}
		aliases[alias] = target
		m.db.SetStringMap("Settings", "aliases", aliases)

		text := formatTrans(m.getTrans("alias_created", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Alias created. Access it with</b> <code>{}</code>"), utils.EscapeHTML(alias))
		_ = msg.Answer(text)
	} else {
		text := formatTrans(m.getTrans("no_command", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Command</b> <code>{}</code> <b>does not exist</b>"), utils.EscapeHTML(cmd))
		_ = msg.Answer(text)
	}
	return nil
}

func (m *SettingsModule) DelAliasCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) != 1 {
		_ = msg.Answer(m.getTrans("delalias_args", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>You must provide the alias name</b>"))
		return nil
	}

	alias := args[0]

	loader := msg.Client.Loader
	if loader == nil {
		return nil
	}

	if !loader.RemoveAlias(alias) {
		text := formatTrans(m.getTrans("no_alias", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Alias</b> <code>{}</code> <b>does not exist</b>"), utils.EscapeHTML(alias))
		_ = msg.Answer(text)
		return nil
	}

	aliases := m.db.GetStringMap("Settings", "aliases", nil)
	delete(aliases, alias)
	m.db.SetStringMap("Settings", "aliases", aliases)

	text := formatTrans(m.getTrans("alias_removed", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Alias</b> <code>{}</code> <b>removed</b>."), utils.EscapeHTML(alias))
	_ = msg.Answer(text)
	return nil
}

func (m *SettingsModule) ClearDBCmd(msg *goroku.Message) error {
	im := m.client.GorokuInline
	if im == nil {
		raw := strings.TrimSpace(utils.GetArgsRaw(msg.Text))
		if raw != "-f" && raw != "--force" {
			_ = msg.Answer("⚠️ <b>This will clear the entire database!</b>\nTo confirm, run: <code>.cleardb -f</code>")
			return nil
		}
		m.db.Reset(make(map[string]map[string]any))
		_ = msg.Answer(m.getTrans("db_cleared", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Database cleared</b>"))
		return nil
	}

	confirmText := m.getTrans("confirm_cleardb", "⚠️ <b>Are you sure, that you want to clear database?</b>")
	markup := [][]inline.Button{
		{
			{
				Text: m.getTrans("cleardb_confirm", "🗑 Clear database"),
				Data: "cleardb_confirm",
				Handler: func(c inline.CallbackQuery) error {
					_ = c.Answer("Clearing database...", false)
					_ = closeForm(c)

					m.db.Reset(make(map[string]map[string]any))

					botAPI := im.GetBotAPI()
					replyMsg := tgbotapi.NewMessage(c.ChatID, m.getTrans("db_cleared", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Database cleared</b>"))
					_, _ = botAPI.Send(replyMsg)
					return nil
				},
			},
			{
				Text: m.getTrans("cancel", "🚫 Cancel"),
				Data: "cleardb_cancel",
				Handler: func(c inline.CallbackQuery) error {
					return closeForm(c)
				},
			},
		},
	}

	_, err := im.Form(confirmText, msg.ChatID, markup)
	return err
}

func (m *SettingsModule) resolveSettingsModule(msg *goroku.Message, modArg string) (*goroku.Modules, goroku.Module, bool) {
	loader := msg.Client.Loader
	if loader == nil {
		_ = msg.Answer("❌ Modules registry not found")
		return nil, nil, false
	}

	mod := loader.LookupByName(modArg)
	if mod == nil {
		text := formatTrans(m.getTrans("mod404", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Watcher {} not found</b>"), modArg)
		_ = msg.Answer(text)
		return nil, nil, false
	}
	return loader, mod, true
}

func findCommandName(mod goroku.Module, cmdArg string) string {
	for cmdName := range mod.Commands() {
		if strings.EqualFold(cmdName, cmdArg) {
			return cmdName
		}
	}
	return ""
}

func toggleStringFold(items []string, value string) ([]string, bool) {
	result := make([]string, 0, len(items)+1)
	removed := false
	for _, item := range items {
		if strings.EqualFold(item, value) {
			removed = true
			continue
		}
		result = append(result, item)
	}
	if removed {
		return result, false
	}
	return append(result, value), true
}

func (m *SettingsModule) ToggleCmdCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) < 2 {
		_ = msg.Answer(m.getTrans("wrong_usage_tcc", "Usage: togglecmd <module> <command> or togglecmd <command>"))
		return nil
	}

	modArg, cmdArg := args[0], args[1]

	loader, mod, ok := m.resolveSettingsModule(msg, modArg)
	if !ok {
		return nil
	}

	moduleKey := mod.Name()
	actualCmd := findCommandName(mod, cmdArg)
	if actualCmd == "" {
		_ = msg.Answer(m.getTrans("cmd404", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Command not found</b>"))
		return nil
	}

	disabledCmds := m.db.GetStringMapStringSlice("goroku.main", "disabled_commands", nil)
	modDisabled := disabledCmds[moduleKey]
	newList, enabled := toggleStringFold(modDisabled, actualCmd)

	if enabled {
		disabledCmds[moduleKey] = newList
		m.db.SetStringMapStringSlice("goroku.main", "disabled_commands", disabledCmds)
		loader.UnregisterCommand(actualCmd)
		_ = msg.Answer(fmt.Sprintf("Command %s disabled in module %s", actualCmd, moduleKey))
	} else {
		if len(newList) == 0 {
			delete(disabledCmds, moduleKey)
		} else {
			disabledCmds[moduleKey] = newList
		}
		m.db.SetStringMapStringSlice("goroku.main", "disabled_commands", disabledCmds)
		loader.RegisterCommand(actualCmd, mod.Commands()[actualCmd])
		_ = msg.Answer(fmt.Sprintf("Command %s enabled in module %s", actualCmd, moduleKey))
	}

	return nil
}

func (m *SettingsModule) ToggleModCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) == 0 {
		_ = msg.Answer(m.getTrans("wrong_usage_tmc", "Usage: togglemod <module>"))
		return nil
	}

	modArg := args[0]
	loader, mod, ok := m.resolveSettingsModule(msg, modArg)
	if !ok {
		return nil
	}

	moduleKey := mod.Name()

	disabled := m.db.GetStringSlice("goroku.main", "disabled_modules", nil)
	newDisabled, enabled := toggleString(disabled, moduleKey)

	if !enabled {
		m.db.SetStringSlice("goroku.main", "disabled_modules", newDisabled)
		for cmdName, handler := range mod.Commands() {
			loader.RegisterCommand(cmdName, handler)
		}
		text := formatTrans(m.getTrans("mod_enabled", "Module {} enabled"), moduleKey)
		_ = msg.Answer(text)
	} else {
		m.db.SetStringSlice("goroku.main", "disabled_modules", newDisabled)
		for cmdName := range mod.Commands() {
			loader.UnregisterCommand(cmdName)
		}
		text := formatTrans(m.getTrans("mod_disabled", "Module {} disabled"), moduleKey)
		_ = msg.Answer(text)
	}

	return nil
}

func (m *SettingsModule) ClearModuleCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) == 0 {
		_ = msg.Answer(m.getTrans("wrong_usage_cmc", "Cleared DB for module {}"))
		return nil
	}

	modArg := args[0]
	moduleKey := modArg

	if loader := msg.Client.Loader; loader != nil {
		if mod := loader.LookupByName(modArg); mod != nil {
			moduleKey = mod.Name()
		}
	}

	m.db.DeleteOwner(moduleKey)

	disabledCmds := m.db.GetStringMapStringSlice("goroku.main", "disabled_commands", nil)
	delete(disabledCmds, moduleKey)
	m.db.SetStringMapStringSlice("goroku.main", "disabled_commands", disabledCmds)

	disabled := m.db.GetStringSlice("goroku.main", "disabled_modules", nil)
	var newDisabled []string
	for _, item := range disabled {
		if item != moduleKey {
			newDisabled = append(newDisabled, item)
		}
	}
	m.db.SetStringSlice("goroku.main", "disabled_modules", newDisabled)

	_ = msg.Answer(fmt.Sprintf("Cleared DB for module %s", moduleKey))
	return nil
}

func (m *SettingsModule) GorokuCmd(msg *goroku.Message) error {
	isPremium := false
	if m.client != nil && m.client.GorokuMe != nil {
		if u := m.client.GorokuMe; u != nil {
			isPremium = u.Premium
		}
	}

	platform := "🪐 <b>Goroku userbot</b>"
	if isPremium {
		platform = utils.GetPlatformEmoji()
	}

	shortHash := utils.GetGitHash()
	if len(shortHash) > 7 {
		shortHash = shortHash[:7]
	}

	commitURLHTML := "Unknown"
	if shortHash != "" {
		commitURLHTML = fmt.Sprintf("<a href=\"https://github.com/gemeguardian/Goroku/commit/%s\">#%s</a>", utils.GetGitHash(), shortHash)
	}

	v := goroku.Version
	verStr := []string{strconv.Itoa(v[0]), strconv.Itoa(v[1]), strconv.Itoa(v[2])}

	libraryStr := fmt.Sprintf("gotd v0.120.0 #%d", tg.Layer)

	template := m.getTrans("goroku", "{} <b>{}.{}.{}</b> <i>{}</i>\n\n<b><tg-emoji emoji-id=5289608677244811430>📁</emoji> <b>goroku-tl:</b> <i>{}</i>\n\n<tg-emoji emoji-id=5228879218363872764>⌨</emoji> <b>Developers: <a href=\"t.me/coddrago\">@coddrago</a>, <a href=\"t.me/zetgo\">@zetgo</a></b>")
	formattedText := formatTrans(template, platform, verStr[0], verStr[1], verStr[2], commitURLHTML, libraryStr)

	branch := goroku.GetVersionBranch()
	if branch != "master" {
		unstableTemplate := m.getTrans("unstable", "\n\n<tg-emoji emoji-id=5355133243773435190>❕</tg-emoji> <b>You are using an unstable branch</b> <code>{}</code><b>!</b>")
		formattedText += formatTrans(unstableTemplate, branch)
	}

	fileURL := "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/goroku_cmd.png"

	var opts []goroku.MsgOption
	if msg.ReplyToMsgID != 0 {
		opts = append(opts, goroku.WithReplyTo(int64(msg.ReplyToMsgID)))
	}

	if msg.Out {
		_ = msg.Delete()
	}

	_, err := m.client.SendFileWithOptions(goroku.ChatRefID(msg.ChatID), fileURL, formattedText, opts...)
	return err
}

func (m *SettingsModule) getInstallationMarkup() [][]inline.Button {
	platforms := []string{"vds", "wsl", "userland"}
	var buttons []inline.Button
	for _, p := range platforms {
		platformName := p
		buttons = append(buttons, inline.Button{
			Text: m.getTrans(platformName, platformName),
			Data: "install_" + platformName,
			Handler: func(c inline.CallbackQuery) error {
				_ = c.Answer("Loading...", false)
				guideKey := platformName + "_install"
				guideText := m.getTrans(guideKey, guideKey)
				markup := c.Manager.GenerateMarkup(m.getInstallationMarkup())
				return c.Edit(guideText, markup)
			},
		})
	}
	return chunkButtons(buttons, 2)
}

func (m *SettingsModule) InstallationCmd(msg *goroku.Message) error {
	args := strings.TrimSpace(utils.GetArgsRaw(msg.Text))

	validArgs := map[string]string{
		"-vds": "vds_install",
		"-wsl": "wsl_install",
		"-ul":  "userland_install",
	}

	if guideKey, ok := validArgs[args]; ok {
		guideText := m.getTrans(guideKey, guideKey)
		_ = msg.Answer(guideText)
		return nil
	}

	im := m.client.GorokuInline
	if im != nil {
		text := m.getTrans("choose_installation", "<tg-emoji emoji-id=5363805650327450240>🪐</tg-emoji> <b>Choose your Goroku installation option:</b>")
		markup := m.getInstallationMarkup()
		photoURL := "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/goroku_installation.png"

		_, err := im.Form(text, msg, markup, inline.WithPhoto(photoURL))
		if err == nil {
			if msg.Out {
				_ = msg.Delete()
			}
			return nil
		}
	}

	photoURL := "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/goroku_installation.png"
	text := m.getTrans("vds_install", "VDS Installation")

	var opts []goroku.MsgOption
	if msg.ReplyToMsgID != 0 {
		opts = append(opts, goroku.WithReplyTo(int64(msg.ReplyToMsgID)))
	}

	if msg.Out {
		_ = msg.Delete()
	}

	_, err := m.client.SendFileWithOptions(goroku.ChatRefID(msg.ChatID), photoURL, text, opts...)
	return err
}

func chunkButtons(buttons []inline.Button, chunkSize int) [][]inline.Button {
	var chunks [][]inline.Button
	for i := 0; i < len(buttons); i += chunkSize {
		end := i + chunkSize
		if end > len(buttons) {
			end = len(buttons)
		}
		chunks = append(chunks, buttons[i:end])
	}
	return chunks
}
