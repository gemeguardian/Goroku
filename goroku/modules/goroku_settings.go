package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/inlineiface"
	"goroku/goroku/utils"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

type GorokuSettings struct {
	goroku.Base
}

func (m *GorokuSettings) Name() string {
	return "GorokuSettings"
}

func (m *GorokuSettings) Strings() map[string]string {
	return map[string]string{
		"name": "GorokuSettings",
	}
}

func (m *GorokuSettings) OnUnload() error { return nil }

func (m *GorokuSettings) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"watchers":               m.WatchersCmd,
		"watcherbl":              m.WatcherBlCmd,
		"watchercmd":             m.WatcherCmdCmd,
		"nonickuser":             m.NoNickUserCmd,
		"nonickchat":             m.NoNickChatCmd,
		"nonickusers":            m.NoNickUsersCmd,
		"nonickchats":            m.NoNickChatsCmd,
		"nonickcmdcmd":           m.NoNickCmdCmd,
		"nonickcmds":             m.NoNickCmdsCmd,
		"settings":               m.SettingsCmd,
		"remove_core_protection": m.RemoveCoreProtectionCmd,
		"enable_core_protection": m.EnableCoreProtectionCmd,
	}
}

func (m *GorokuSettings) getWatchers() ([]string, map[string]any) {
	disabled := m.DB.GetAnyMap("goroku.main", "disabled_watchers", nil)

	loader := m.Client.Loader
	if loader == nil {
		return nil, disabled
	}

	namesMap := make(map[string]bool)
	for _, w := range loader.GetWatchers() {
		namesMap[w.ModuleName] = true
	}

	var names []string
	for k := range namesMap {
		names = append(names, k)
	}

	return names, disabled
}

// WatchersCmd lists all registered watchers and their enabled/disabled status.
func (m *GorokuSettings) WatchersCmd(msg *goroku.Message) error {
	watchers, disabledWatchers := m.getWatchers()
	disabled := map[string]bool{}
	for k := range disabledWatchers {
		disabled[strings.ToLower(k)] = true
	}

	var lines []string
	for _, name := range watchers {
		if disabled[strings.ToLower(name)] {
			lines = append(lines, "💢 "+name)
		} else {
			lines = append(lines, "♻️ "+name)
		}
	}

	if len(lines) == 0 {
		template := m.T("watchers", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотрители:</b>\n\n<blockquote expandable><b>{0}</b></blockquote>")
		return msg.Answer(formatTrans(template, "No watchers registered."))
	}

	template := m.T("watchers", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотрители:</b>\n\n<blockquote expandable><b>{0}</b></blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// WatcherBlCmd toggles a watcher's blacklist for the current chat.
func (m *GorokuSettings) WatcherBlCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.T("args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи имя смотрителя</b>"))
	}
	watcherNameInput := strings.TrimSpace(parts[1])

	watchers, disabled := m.getWatchers()
	var realName string
	for _, w := range watchers {
		if strings.EqualFold(w, watcherNameInput) {
			realName = w
			break
		}
	}
	if realName == "" {
		template := m.T("mod404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Смотритель {0} не найден</b>")
		return msg.Answer(formatTrans(template, watcherNameInput))
	}

	chatID := fmt.Sprintf("%d", msg.ChatID)

	if chats, ok := disabled[realName]; ok {
		if chatList, ok := chats.([]any); ok {
			found := false
			var newList []any
			for _, c := range chatList {
				if fmt.Sprintf("%v", c) == chatID {
					found = true
				} else {
					newList = append(newList, c)
				}
			}
			if found {
				if len(newList) == 0 {
					delete(disabled, realName)
				} else {
					disabled[realName] = newList
				}
				if err := m.DB.SetAnyMap("goroku.main", "disabled_watchers", disabled); err != nil {
					return err
				}
				template := m.T("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
				return msg.Answer(formatTrans(template, realName) + " <b>in current chat</b>")
			}
			chatList = append(chatList, chatID)
			disabled[realName] = chatList
		}
	} else {
		disabled[realName] = []any{chatID}
	}

	if err := m.DB.SetAnyMap("goroku.main", "disabled_watchers", disabled); err != nil {
		return err
	}
	template := m.T("disabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>выключен</u></b>")
	return msg.Answer(formatTrans(template, realName) + " <b>in current chat</b>")
}

// WatcherCmdCmd toggles a watcher globally or with filters.
func (m *GorokuSettings) WatcherCmdCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.T("args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи имя смотрителя</b>"))
	}
	args := strings.TrimSpace(parts[1])

	chats, pm, out, incoming := false, false, false, false
	if strings.Contains(args, "-c") {
		args = strings.ReplaceAll(args, "-c", "")
		chats = true
	}
	if strings.Contains(args, "-p") {
		args = strings.ReplaceAll(args, "-p", "")
		pm = true
	}
	if strings.Contains(args, "-o") {
		args = strings.ReplaceAll(args, "-o", "")
		out = true
	}
	if strings.Contains(args, "-i") {
		args = strings.ReplaceAll(args, "-i", "")
		incoming = true
	}
	watcherNameInput := strings.TrimSpace(args)

	watchers, disabled := m.getWatchers()
	var realName string
	for _, w := range watchers {
		if strings.EqualFold(w, watcherNameInput) {
			realName = w
			break
		}
	}
	if realName == "" {
		template := m.T("mod404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Смотритель {0} не найден</b>")
		return msg.Answer(formatTrans(template, watcherNameInput))
	}

	if chats || pm || out || incoming {
		var filters []any
		if chats {
			filters = append(filters, "only_chats")
		}
		if pm {
			filters = append(filters, "only_pm")
		}
		if out {
			filters = append(filters, "out")
		}
		if incoming {
			filters = append(filters, "in")
		}
		disabled[realName] = filters
		if err := m.DB.SetAnyMap("goroku.main", "disabled_watchers", disabled); err != nil {
			return err
		}
		template := m.T("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
		return msg.Answer(formatTrans(template, realName) + fmt.Sprintf(" (<code>%v</code>)", filters))
	}

	if wval, ok := disabled[realName]; ok {
		if wlist, ok := wval.([]any); ok && len(wlist) == 1 && fmt.Sprintf("%v", wlist[0]) == "*" {
			delete(disabled, realName)
			if err := m.DB.SetAnyMap("goroku.main", "disabled_watchers", disabled); err != nil {
				return err
			}
			template := m.T("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
			return msg.Answer(formatTrans(template, realName))
		}
	}
	disabled[realName] = []any{"*"}
	if err := m.DB.SetAnyMap("goroku.main", "disabled_watchers", disabled); err != nil {
		return err
	}
	template := m.T("disabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>выключен</u></b>")
	return msg.Answer(formatTrans(template, realName))
}

func toggleInt64(items []int64, value int64) ([]int64, bool) {
	result := make([]int64, 0, len(items)+1)
	removed := false
	for _, item := range items {
		if item == value {
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

func toggleString(items []string, value string) ([]string, bool) {
	result := make([]string, 0, len(items)+1)
	removed := false
	for _, item := range items {
		if item == value {
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

func onOffState(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// NoNickUserCmd toggles no-nick for a replied-to user.
func (m *GorokuSettings) NoNickUserCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil {
		return msg.Answer(m.T("reply_required", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нужен ответ на сообщение</b>"))
	}

	u := reply.SenderID
	users := m.DB.GetInt64Slice("goroku.main", "nonickusers", nil)
	newList, enabled := toggleInt64(users, u)
	if err := m.DB.SetInt64Slice("goroku.main", "nonickusers", newList); err != nil {
		return err
	}

	template := m.T("user_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для этого пользователя: {0}</b>")
	return msg.Answer(formatTrans(template, onOffState(enabled)))
}

// NoNickChatCmd toggles no-nick for the current chat.
func (m *GorokuSettings) NoNickChatCmd(msg *goroku.Message) error {
	if msg.IsPrivate {
		return msg.Answer(m.T("private_not_allowed", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нельзя использовать в личных сообщениях</b>"))
	}

	chats := m.DB.GetInt64Slice("goroku.main", "nonickchats", nil)
	newList, enabled := toggleInt64(chats, msg.ChatID)
	if err := m.DB.SetInt64Slice("goroku.main", "nonickchats", newList); err != nil {
		return err
	}

	chatTitle := fmt.Sprintf("Chat %d", msg.ChatID)
	if entity, err := m.Client.GetEntity(msg.ChatID, 0, false); err == nil {
		if displayName := getDisplayName(entity); displayName != "" {
			chatTitle = displayName
		}
	}

	template := m.T("cmd_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для {0}: {1}</b>")
	return msg.Answer(formatTrans(template, utils.EscapeHTML(chatTitle), onOffState(enabled)))
}

// NoNickUsersCmd lists all users with no-nick enabled.
func (m *GorokuSettings) NoNickUsersCmd(msg *goroku.Message) error {
	users := m.DB.GetInt64Slice("goroku.main", "nonickusers", nil)

	var lines []string
	var validUsers []int64
	for _, u := range users {
		entity, err := m.Client.GetEntity(u, 0, false)
		if err != nil {
			continue
		}
		validUsers = append(validUsers, u)
		displayName := getDisplayName(entity)
		if displayName == "" {
			displayName = fmt.Sprintf("User%d", u)
		}
		lines = append(lines, fmt.Sprintf("▫️ <b><a href=\"tg://user?id=%d\">%s</a></b>", u, utils.EscapeHTML(displayName)))
	}

	if len(users) != len(validUsers) {
		if err := m.DB.SetInt64Slice("goroku.main", "nonickusers", validUsers); err != nil {
			return err
		}
	}

	if len(lines) == 0 {
		return msg.Answer(m.T("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	template := m.T("user_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick пользователи:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// NoNickChatsCmd lists all chats with no-nick enabled.
func (m *GorokuSettings) NoNickChatsCmd(msg *goroku.Message) error {
	chats := m.DB.GetInt64Slice("goroku.main", "nonickchats", nil)

	var lines []string
	var validChats []int64
	for _, chatID := range chats {
		if chatID == 0 {
			continue
		}
		entity, err := m.Client.GetEntity(chatID, 0, false)
		if err != nil {
			continue
		}
		validChats = append(validChats, chatID)
		displayName := getDisplayName(entity)
		if displayName == "" {
			displayName = fmt.Sprintf("Chat%d", chatID)
		}
		lines = append(lines, fmt.Sprintf("▫️ <b><a href=\"%s\">%s</a></b>", utils.GetEntityURL(entity, false), utils.EscapeHTML(displayName)))
	}

	if len(chats) != len(validChats) {
		if err := m.DB.SetInt64Slice("goroku.main", "nonickchats", validChats); err != nil {
			return err
		}
	}

	if len(lines) == 0 {
		return msg.Answer(m.T("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	template := m.T("user_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick пользователи:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// NoNickCmdCmd toggles command whitelisting for nickname enforcement.
func (m *GorokuSettings) NoNickCmdCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.T("no_cmd", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи команду</b>"))
	}
	cmdInput := strings.TrimSpace(parts[1])

	loader := m.Client.Loader
	if loader == nil {
		return msg.Answer("❌ Loader not found.")
	}
	if _, exists := loader.Dispatch(cmdInput); !exists {
		return msg.Answer(m.T("cmd404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Команда не найдена</b>"))
	}

	cmds := m.DB.GetStringSlice("goroku.main", "nonickcmds", nil)
	newList, enabled := toggleString(cmds, cmdInput)
	if err := m.DB.SetStringSlice("goroku.main", "nonickcmds", newList); err != nil {
		return err
	}

	prefix := m.DB.GetString("goroku.main", "command_prefix", ".")

	template := m.T("cmd_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для {0}: {1}</b>")
	return msg.Answer(formatTrans(template, prefix+cmdInput, onOffState(enabled)))
}

// NoNickCmdsCmd lists all commands whitelisted for nickname enforcement.
func (m *GorokuSettings) NoNickCmdsCmd(msg *goroku.Message) error {
	cmds := m.DB.GetStringSlice("goroku.main", "nonickcmds", nil)
	if len(cmds) == 0 {
		return msg.Answer(m.T("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	prefix := m.DB.GetString("goroku.main", "command_prefix", ".")

	var lines []string
	for _, c := range cmds {
		lines = append(lines, fmt.Sprintf("▫️ <code>%s%v</code>", prefix, c))
	}

	template := m.T("cmd_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick команды:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// SettingsCmd launches the interactive inline dashboard.
func (m *GorokuSettings) SettingsCmd(msg *goroku.Message) error {
	im := m.Client.GorokuInline
	if im == nil {
		return msg.Answer("❌ Inline manager is not initialized.")
	}

	_, err := im.Form(
		m.getSettingsText(),
		msg,
		m.getSettingsMarkup(im),
	)
	return err
}

func (m *GorokuSettings) getSettingsText() string {
	noNick := m.DB.GetBool("goroku.main", "no_nickname", false)
	grep := m.DB.GetBool("goroku.main", "grep", false)
	inlineLogs := m.DB.GetBool("goroku.main", "inlinelogs", true)

	return fmt.Sprintf(
		m.T("inline_settings", "⚙️ <b>Goroku Settings</b>")+"\n\n"+
			"NoNick: <b>%v</b>\n"+
			"Grep: <b>%v</b>\n"+
			"InlineLogs: <b>%v</b>",
		noNick, grep, inlineLogs,
	)
}

func (m *GorokuSettings) getSettingsMarkup(im inlineiface.InlineManager) [][]inline.Button {
	noNick := m.DB.GetBool("goroku.main", "no_nickname", false)
	grep := m.DB.GetBool("goroku.main", "grep", false)
	inlineLogs := m.DB.GetBool("goroku.main", "inlinelogs", true)
	suggestSub := m.DB.GetBool("goroku.main", "suggest_subscribe", true)

	var btnNoNick inline.Button
	if noNick {
		btnNoNick = inline.Button{
			Text: "✅ NoNick",
			Data: "hset_nonick_off",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "no_nickname", false); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnNoNick = inline.Button{
			Text: "🚫 NoNick",
			Data: "hset_nonick_on",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "no_nickname", true); err != nil {
					return err
				}
				prefix := m.DB.GetString("goroku.main", "command_prefix", ".")
				if prefix == "." {
					_ = c.Answer(m.T("nonick_warning", "⚠️ WARNING: Enforcing nickname verification with a dot prefix will ignore commands unless you mention the bot or whitelist yourself/chat/commands!"), true)
				} else {
					_ = c.Answer("Configuration value saved!", false)
				}
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	var btnGrep inline.Button
	if grep {
		btnGrep = inline.Button{
			Text: "✅ Grep",
			Data: "hset_grep_off",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "grep", false); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnGrep = inline.Button{
			Text: "🚫 Grep",
			Data: "hset_grep_on",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "grep", true); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	var btnInlineLogs inline.Button
	if inlineLogs {
		btnInlineLogs = inline.Button{
			Text: "✅ InlineLogs",
			Data: "hset_inlinelogs_off",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "inlinelogs", false); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnInlineLogs = inline.Button{
			Text: "🚫 InlineLogs",
			Data: "hset_inlinelogs_on",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "inlinelogs", true); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	var btnSuggest inline.Button
	if suggestSub {
		btnSuggest = inline.Button{
			Text: m.T("suggest_subscribe", "🔔 Suggest Subscribe"),
			Data: "hset_suggest_off",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "suggest_subscribe", false); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnSuggest = inline.Button{
			Text: m.T("do_not_suggest_subscribe", "🔕 Do Not Suggest Subscribe"),
			Data: "hset_suggest_on",
			Handler: func(c inline.CallbackQuery) error {
				if err := m.DB.SetBool("goroku.main", "suggest_subscribe", true); err != nil {
					return err
				}
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	btnRestart := inline.Button{
		Text: m.T("btn_restart", "🔄 Restart"),
		Data: "hset_restart_confirm",
		Handler: func(c inline.CallbackQuery) error {
			confirmMarkup := [][]inline.Button{
				{
					{
						Text: "🔄 " + m.T("btn_restart", "Restart"),
						Data: "hset_restart_exec",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Your userbot is being restarted...", true)
							_ = closeForm(c2)
							go func() {
								time.Sleep(1 * time.Second)
								goroku.Restart()
							}()
							return nil
						},
					},
					{
						Text: "🚫 " + m.T("btn_no", "Cancel"),
						Data: "hset_restart_cancel",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Restart cancelled.", false)
							return c2.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
						},
					},
				},
			}
			return c.Edit(m.T("confirm_restart", "🔄 <b>Confirm Restart?</b>"), im.GenerateMarkup(confirmMarkup))
		},
	}

	btnUpdate := inline.Button{
		Text: m.T("btn_update", "🪂 Update"),
		Data: "hset_update_confirm",
		Handler: func(c inline.CallbackQuery) error {
			confirmMarkup := [][]inline.Button{
				{
					{
						Text: "🪂 " + m.T("btn_update", "Update"),
						Data: "hset_update_exec",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Updating userbot...", true)
							_ = closeForm(c2)
							go func() {
								loader := m.Client.Loader
								if loader != nil {
									msg := &goroku.Message{
										ChatID: m.Client.TGIDValue(),
										Client: m.Client,
										Out:    true,
									}
									_ = goroku.InvokeCommand(loader, msg, "update", "-f")
								}
							}()
							return nil
						},
					},
					{
						Text: "🚫 " + m.T("btn_no", "Cancel"),
						Data: "hset_update_cancel",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Update cancelled.", false)
							return c2.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
						},
					},
				},
			}
			return c.Edit(m.T("confirm_update", "🪂 <b>Confirm Update?</b>"), im.GenerateMarkup(confirmMarkup))
		},
	}

	btnClose := inline.Button{
		Text: m.T("close_menu", "🚫 Close"),
		Data: "hset_close",
		Handler: func(c inline.CallbackQuery) error {
			_ = c.Answer("Settings closed.", false)
			return closeForm(c)
		},
	}

	return [][]inline.Button{
		{btnNoNick, btnGrep, btnInlineLogs},
		{btnSuggest},
		{btnRestart, btnUpdate},
		{btnClose},
	}
}

func (m *GorokuSettings) RemoveCoreProtectionCmd(msg *goroku.Message) error {
	isRemoved := m.DB.GetBool("goroku.main", "remove_core_protection", false)
	if isRemoved {
		return msg.Answer(m.T("core_protection_already_removed", "⚠️ Core protection already removed"))
	}

	im := m.Client.GorokuInline
	if im == nil {
		return msg.Answer("❌ Inline manager is not initialized.")
	}

	_, err := im.Form(
		m.T("core_protection_confirm", "⚠️ <b>Are you sure you want to disable core protection?</b>"),
		msg,
		[][]inline.Button{
			{
				{
					Text: m.T("core_protection_btn", "🔓 Disable"),
					Data: "hset_coreprot_remove",
					Handler: func(c inline.CallbackQuery) error {
						if err := m.DB.SetBool("goroku.main", "remove_core_protection", true); err != nil {
							return err
						}
						_ = c.Answer(m.T("core_protection_removed", "✅ Core protection removed"), false)
						_ = closeForm(c)
						return nil
					},
				},
				{
					Text:    m.T("btn_no", "🚫 No"),
					Data:    "hset_coreprot_cancel",
					Handler: closeForm,
				},
			},
		},
	)
	return err
}

func (m *GorokuSettings) EnableCoreProtectionCmd(msg *goroku.Message) error {
	isRemoved := m.DB.GetBool("goroku.main", "remove_core_protection", false)
	if !isRemoved {
		return msg.Answer(m.T("core_protection_already_enabled", "⚠️ Core protection already enabled"))
	}

	im := m.Client.GorokuInline
	if im == nil {
		return msg.Answer("❌ Inline manager is not initialized.")
	}

	_, err := im.Form(
		m.T("core_protection_confirm_e", "⚠️ <b>Are you sure you want to enable core protection?</b>"),
		msg,
		[][]inline.Button{
			{
				{
					Text: m.T("core_protection_e_btn", "🔒 Enable"),
					Data: "hset_coreprot_enable",
					Handler: func(c inline.CallbackQuery) error {
						if err := m.DB.SetBool("goroku.main", "remove_core_protection", false); err != nil {
							return err
						}
						_ = c.Answer(m.T("core_protection_enabled", "✅ Core protection enabled"), false)
						_ = closeForm(c)
						return nil
					},
				},
				{
					Text:    m.T("btn_no", "🚫 No"),
					Data:    "hset_coreprot_cancel",
					Handler: closeForm,
				},
			},
		},
	)
	return err
}

func getDisplayName(entity any) string {
	if entity == nil {
		return ""
	}
	switch e := entity.(type) {
	case *tg.User:
		name := e.FirstName
		if e.LastName != "" {
			name += " " + e.LastName
		}
		return name
	case *tg.Chat:
		return e.Title
	case *tg.Channel:
		return e.Title
	case *tg.ChatForbidden:
		return e.Title
	case *tg.ChannelForbidden:
		return e.Title
	}
	return ""
}
