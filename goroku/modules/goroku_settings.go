package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

type GorokuSettings struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *GorokuSettings) Name() string {
	return "GorokuSettings"
}

func (m *GorokuSettings) Strings() map[string]string {
	return map[string]string{
		"name": "GorokuSettings",
	}
}

func (m *GorokuSettings) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *GorokuSettings) ClientReady() error { return nil }
func (m *GorokuSettings) OnUnload() error    { return nil }
func (m *GorokuSettings) OnDlmod() error     { return nil }

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

func (m *GorokuSettings) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuSettings) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *GorokuSettings) getWatchers() ([]string, map[string]interface{}) {
	raw := m.db.Get("goroku.main", "disabled_watchers", map[string]interface{}{})
	disabled := map[string]interface{}{}
	if dw, ok := raw.(map[string]interface{}); ok {
		disabled = dw
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
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
		template := m.getTrans("watchers", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотрители:</b>\n\n<blockquote expandable><b>{0}</b></blockquote>")
		return msg.Answer(formatTrans(template, "No watchers registered."))
	}

	template := m.getTrans("watchers", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотрители:</b>\n\n<blockquote expandable><b>{0}</b></blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// WatcherBlCmd toggles a watcher's blacklist for the current chat.
func (m *GorokuSettings) WatcherBlCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи имя смотрителя</b>"))
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
		template := m.getTrans("mod404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Смотритель {0} не найден</b>")
		return msg.Answer(formatTrans(template, watcherNameInput))
	}

	chatID := fmt.Sprintf("%d", msg.ChatID)

	if chats, ok := disabled[realName]; ok {
		if chatList, ok := chats.([]interface{}); ok {
			found := false
			var newList []interface{}
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
				m.db.Set("goroku.main", "disabled_watchers", disabled)
				template := m.getTrans("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
				return msg.Answer(formatTrans(template, realName) + " <b>in current chat</b>")
			}
			chatList = append(chatList, chatID)
			disabled[realName] = chatList
		}
	} else {
		disabled[realName] = []interface{}{chatID}
	}

	m.db.Set("goroku.main", "disabled_watchers", disabled)
	template := m.getTrans("disabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>выключен</u></b>")
	return msg.Answer(formatTrans(template, realName) + " <b>in current chat</b>")
}

// WatcherCmdCmd toggles a watcher globally or with filters.
func (m *GorokuSettings) WatcherCmdCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи имя смотрителя</b>"))
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
		template := m.getTrans("mod404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Смотритель {0} не найден</b>")
		return msg.Answer(formatTrans(template, watcherNameInput))
	}

	if chats || pm || out || incoming {
		var filters []interface{}
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
		m.db.Set("goroku.main", "disabled_watchers", disabled)
		template := m.getTrans("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
		return msg.Answer(formatTrans(template, realName) + fmt.Sprintf(" (<code>%v</code>)", filters))
	}

	if wval, ok := disabled[realName]; ok {
		if wlist, ok := wval.([]interface{}); ok && len(wlist) == 1 && fmt.Sprintf("%v", wlist[0]) == "*" {
			delete(disabled, realName)
			m.db.Set("goroku.main", "disabled_watchers", disabled)
			template := m.getTrans("enabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>включен</u></b>")
			return msg.Answer(formatTrans(template, realName))
		}
	}
	disabled[realName] = []interface{}{"*"}
	m.db.Set("goroku.main", "disabled_watchers", disabled)
	template := m.getTrans("disabled", "<tg-emoji emoji-id=5424885441100782420>👀</tg-emoji> <b>Смотритель {0} теперь <u>выключен</u></b>")
	return msg.Answer(formatTrans(template, realName))
}

// NoNickUserCmd toggles no-nick for a replied-to user.
func (m *GorokuSettings) NoNickUserCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil {
		return msg.Answer(m.getTrans("reply_required", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нужен ответ на сообщение</b>"))
	}

	u := reply.SenderID
	raw := m.db.Get("goroku.main", "nonickusers", []interface{}{})
	var users []int64
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			var id int64
			switch v := item.(type) {
			case float64:
				id = int64(v)
			case int64:
				id = v
			}
			if id != 0 {
				users = append(users, id)
			}
		}
	}

	found := false
	var newList []interface{}
	for _, id := range users {
		if id == u {
			found = true
		} else {
			newList = append(newList, id)
		}
	}

	var state string
	if found {
		m.db.Set("goroku.main", "nonickusers", newList)
		state = "off"
	} else {
		newList = append(newList, u)
		m.db.Set("goroku.main", "nonickusers", newList)
		state = "on"
	}

	template := m.getTrans("user_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для этого пользователя: {0}</b>")
	return msg.Answer(formatTrans(template, state))
}

// NoNickChatCmd toggles no-nick for the current chat.
func (m *GorokuSettings) NoNickChatCmd(msg *goroku.Message) error {
	if msg.IsPrivate {
		return msg.Answer(m.getTrans("private_not_allowed", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нельзя использовать в личных сообщениях</b>"))
	}

	chatIDStr := fmt.Sprintf("%d", msg.ChatID)
	raw := m.db.Get("goroku.main", "nonickchats", []interface{}{})
	chats := []interface{}{}
	if cl, ok := raw.([]interface{}); ok {
		chats = cl
	}

	found := false
	var newList []interface{}
	for _, c := range chats {
		if fmt.Sprintf("%v", c) == chatIDStr {
			found = true
		} else {
			newList = append(newList, c)
		}
	}

	var state string
	if found {
		m.db.Set("goroku.main", "nonickchats", newList)
		state = "off"
	} else {
		newList = append(chats, chatIDStr)
		m.db.Set("goroku.main", "nonickchats", newList)
		state = "on"
	}

	chatTitle := fmt.Sprintf("Chat %d", msg.ChatID)
	if entity, err := m.client.GetEntity(msg.ChatID, 0, false); err == nil {
		if displayName := getDisplayName(entity); displayName != "" {
			chatTitle = displayName
		}
	}

	template := m.getTrans("cmd_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для {0}: {1}</b>")
	return msg.Answer(formatTrans(template, utils.EscapeHTML(chatTitle), state))
}

// NoNickUsersCmd lists all users with no-nick enabled.
func (m *GorokuSettings) NoNickUsersCmd(msg *goroku.Message) error {
	raw := m.db.Get("goroku.main", "nonickusers", []interface{}{})
	var users []int64
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			var id int64
			switch v := item.(type) {
			case float64:
				id = int64(v)
			case int64:
				id = v
			}
			if id != 0 {
				users = append(users, id)
			}
		}
	}

	var lines []string
	var validUsers []interface{}
	for _, u := range users {
		entity, err := m.client.GetEntity(u, 0, false)
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
		m.db.Set("goroku.main", "nonickusers", validUsers)
	}

	if len(lines) == 0 {
		return msg.Answer(m.getTrans("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	template := m.getTrans("user_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick пользователи:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// NoNickChatsCmd lists all chats with no-nick enabled.
func (m *GorokuSettings) NoNickChatsCmd(msg *goroku.Message) error {
	raw := m.db.Get("goroku.main", "nonickchats", []interface{}{})
	var chats []interface{}
	if cl, ok := raw.([]interface{}); ok {
		chats = cl
	}

	var lines []string
	var validChats []interface{}
	for _, c := range chats {
		var chatID int64
		switch v := c.(type) {
		case float64:
			chatID = int64(v)
		case int64:
			chatID = v
		case string:
			fmt.Sscan(v, &chatID)
		}

		entity, err := m.client.GetEntity(chatID, 0, false)
		if err != nil {
			continue
		}
		validChats = append(validChats, c)
		displayName := getDisplayName(entity)
		if displayName == "" {
			displayName = fmt.Sprintf("Chat%d", chatID)
		}
		lines = append(lines, fmt.Sprintf("▫️ <b><a href=\"%s\">%s</a></b>", utils.GetEntityURL(entity, false), utils.EscapeHTML(displayName)))
	}

	if len(chats) != len(validChats) {
		m.db.Set("goroku.main", "nonickchats", validChats)
	}

	if len(lines) == 0 {
		return msg.Answer(m.getTrans("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	template := m.getTrans("user_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick пользователи:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// NoNickCmdCmd toggles command whitelisting for nickname enforcement.
func (m *GorokuSettings) NoNickCmdCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_cmd", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи команду</b>"))
	}
	cmdInput := strings.TrimSpace(parts[1])

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return msg.Answer("❌ Loader not found.")
	}
	if _, exists := loader.Dispatch(cmdInput); !exists {
		return msg.Answer(m.getTrans("cmd404", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Команда не найдена</b>"))
	}

	raw := m.db.Get("goroku.main", "nonickcmds", []interface{}{})
	cmds := []interface{}{}
	if cl, ok := raw.([]interface{}); ok {
		cmds = cl
	}

	found := false
	var newList []interface{}
	for _, c := range cmds {
		if fmt.Sprintf("%v", c) == cmdInput {
			found = true
		} else {
			newList = append(newList, c)
		}
	}

	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefix = val
	}

	var state string
	if found {
		m.db.Set("goroku.main", "nonickcmds", newList)
		state = "off"
	} else {
		newList = append(cmds, cmdInput)
		m.db.Set("goroku.main", "nonickcmds", newList)
		state = "on"
	}

	template := m.getTrans("cmd_nn", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>Состояние NoNick для {0}: {1}</b>")
	return msg.Answer(formatTrans(template, prefix+cmdInput, state))
}

// NoNickCmdsCmd lists all commands whitelisted for nickname enforcement.
func (m *GorokuSettings) NoNickCmdsCmd(msg *goroku.Message) error {
	raw := m.db.Get("goroku.main", "nonickcmds", []interface{}{})
	cmds := []interface{}{}
	if cl, ok := raw.([]interface{}); ok {
		cmds = cl
	}
	if len(cmds) == 0 {
		return msg.Answer(m.getTrans("nothing", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Список пуст</b>"))
	}

	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefix = val
	}

	var lines []string
	for _, c := range cmds {
		lines = append(lines, fmt.Sprintf("▫️ <code>%s%v</code>", prefix, c))
	}

	template := m.getTrans("cmd_nn_list", "<tg-emoji emoji-id=5469791106591890404>🪄</tg-emoji> <b>NoNick команды:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

// SettingsCmd launches the interactive inline dashboard.
func (m *GorokuSettings) SettingsCmd(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
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
	noNick := false
	if v, ok := m.db.Get("goroku.main", "no_nickname", false).(bool); ok {
		noNick = v
	}
	grep := false
	if v, ok := m.db.Get("goroku.main", "grep", false).(bool); ok {
		grep = v
	}
	inlineLogs := true
	if v, ok := m.db.Get("goroku.main", "inlinelogs", true).(bool); ok {
		inlineLogs = v
	}

	return fmt.Sprintf(
		m.getTrans("inline_settings", "⚙️ <b>Goroku Settings</b>")+"\n\n"+
			"NoNick: <b>%v</b>\n"+
			"Grep: <b>%v</b>\n"+
			"InlineLogs: <b>%v</b>",
		noNick, grep, inlineLogs,
	)
}

func (m *GorokuSettings) getSettingsMarkup(im *inline.InlineManager) [][]inline.Button {
	noNick := false
	if v, ok := m.db.Get("goroku.main", "no_nickname", false).(bool); ok {
		noNick = v
	}
	grep := false
	if v, ok := m.db.Get("goroku.main", "grep", false).(bool); ok {
		grep = v
	}
	inlineLogs := true
	if v, ok := m.db.Get("goroku.main", "inlinelogs", true).(bool); ok {
		inlineLogs = v
	}
	suggestSub := true
	if v, ok := m.db.Get("goroku.main", "suggest_subscribe", true).(bool); ok {
		suggestSub = v
	}

	var btnNoNick inline.Button
	if noNick {
		btnNoNick = inline.Button{
			Text: "✅ NoNick",
			Data: "hset_nonick_off",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "no_nickname", false)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnNoNick = inline.Button{
			Text: "🚫 NoNick",
			Data: "hset_nonick_on",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "no_nickname", true)
				prefix := "."
				if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
					prefix = val
				}
				if prefix == "." {
					_ = c.Answer(m.getTrans("nonick_warning", "⚠️ WARNING: Enforcing nickname verification with a dot prefix will ignore commands unless you mention the bot or whitelist yourself/chat/commands!"), true)
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
				m.db.Set("goroku.main", "grep", false)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnGrep = inline.Button{
			Text: "🚫 Grep",
			Data: "hset_grep_on",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "grep", true)
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
				m.db.Set("goroku.main", "inlinelogs", false)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnInlineLogs = inline.Button{
			Text: "🚫 InlineLogs",
			Data: "hset_inlinelogs_on",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "inlinelogs", true)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	var btnSuggest inline.Button
	if suggestSub {
		btnSuggest = inline.Button{
			Text: m.getTrans("suggest_subscribe", "🔔 Suggest Subscribe"),
			Data: "hset_suggest_off",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "suggest_subscribe", false)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	} else {
		btnSuggest = inline.Button{
			Text: m.getTrans("do_not_suggest_subscribe", "🔕 Do Not Suggest Subscribe"),
			Data: "hset_suggest_on",
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.main", "suggest_subscribe", true)
				_ = c.Answer("Configuration value saved!", false)
				return c.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
			},
		}
	}

	btnRestart := inline.Button{
		Text: m.getTrans("btn_restart", "🔄 Restart"),
		Data: "hset_restart_confirm",
		Handler: func(c inline.CallbackQuery) error {
			confirmMarkup := [][]inline.Button{
				{
					{
						Text: "🔄 " + m.getTrans("btn_restart", "Restart"),
						Data: "hset_restart_exec",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Your userbot is being restarted...", true)
							if c2.BotMessage != nil {
								_, _ = c2.BotMessage.Delete()
							}
							go func() {
								time.Sleep(1 * time.Second)
								goroku.Restart()
							}()
							return nil
						},
					},
					{
						Text: "🚫 " + m.getTrans("btn_no", "Cancel"),
						Data: "hset_restart_cancel",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Restart cancelled.", false)
							return c2.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
						},
					},
				},
			}
			return c.Edit(m.getTrans("confirm_restart", "🔄 <b>Confirm Restart?</b>"), im.GenerateMarkup(confirmMarkup))
		},
	}

	btnUpdate := inline.Button{
		Text: m.getTrans("btn_update", "🪂 Update"),
		Data: "hset_update_confirm",
		Handler: func(c inline.CallbackQuery) error {
			confirmMarkup := [][]inline.Button{
				{
					{
						Text: "🪂 " + m.getTrans("btn_update", "Update"),
						Data: "hset_update_exec",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Updating userbot...", true)
							if c2.BotMessage != nil {
								_, _ = c2.BotMessage.Delete()
							}
							go func() {
								loader, ok := m.client.Loader.(*goroku.Modules)
								if ok && loader != nil {
									msg := &goroku.Message{
										ChatID: m.client.TGID,
										Client: m.client,
										Out:    true,
									}
									_ = goroku.InvokeCommand(loader, msg, "update", "-f")
								}
							}()
							return nil
						},
					},
					{
						Text: "🚫 " + m.getTrans("btn_no", "Cancel"),
						Data: "hset_update_cancel",
						Handler: func(c2 inline.CallbackQuery) error {
							_ = c2.Answer("Update cancelled.", false)
							return c2.Edit(m.getSettingsText(), im.GenerateMarkup(m.getSettingsMarkup(im)))
						},
					},
				},
			}
			return c.Edit(m.getTrans("confirm_update", "🪂 <b>Confirm Update?</b>"), im.GenerateMarkup(confirmMarkup))
		},
	}

	btnClose := inline.Button{
		Text: m.getTrans("close_menu", "🚫 Close"),
		Data: "hset_close",
		Handler: func(c inline.CallbackQuery) error {
			_ = c.Answer("Settings closed.", false)
			if c.BotMessage != nil {
				_, _ = c.BotMessage.Delete()
			}
			return nil
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
	isRemoved := false
	if val, ok := m.db.Get("goroku.main", "remove_core_protection", false).(bool); ok {
		isRemoved = val
	}
	if isRemoved {
		return msg.Answer(m.getTrans("core_protection_already_removed", "⚠️ Core protection already removed"))
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return msg.Answer("❌ Inline manager is not initialized.")
	}

	_, err := im.Form(
		m.getTrans("core_protection_confirm", "⚠️ <b>Are you sure you want to disable core protection?</b>"),
		msg,
		[][]inline.Button{
			{
				{
					Text: m.getTrans("core_protection_btn", "🔓 Disable"),
					Data: "hset_coreprot_remove",
					Handler: func(c inline.CallbackQuery) error {
						m.db.Set("goroku.main", "remove_core_protection", true)
						_ = c.Answer(m.getTrans("core_protection_removed", "✅ Core protection removed"), false)
						if c.BotMessage != nil {
							_, _ = c.BotMessage.Delete()
						}
						return nil
					},
				},
				{
					Text:    m.getTrans("btn_no", "🚫 No"),
					Data:    "hset_coreprot_cancel",
					Handler: closeForm,
				},
			},
		},
	)
	return err
}

func (m *GorokuSettings) EnableCoreProtectionCmd(msg *goroku.Message) error {
	isRemoved := false
	if val, ok := m.db.Get("goroku.main", "remove_core_protection", false).(bool); ok {
		isRemoved = val
	}
	if !isRemoved {
		return msg.Answer(m.getTrans("core_protection_already_enabled", "⚠️ Core protection already enabled"))
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return msg.Answer("❌ Inline manager is not initialized.")
	}

	_, err := im.Form(
		m.getTrans("core_protection_confirm_e", "⚠️ <b>Are you sure you want to enable core protection?</b>"),
		msg,
		[][]inline.Button{
			{
				{
					Text: m.getTrans("core_protection_e_btn", "🔒 Enable"),
					Data: "hset_coreprot_enable",
					Handler: func(c inline.CallbackQuery) error {
						m.db.Set("goroku.main", "remove_core_protection", false)
						_ = c.Answer(m.getTrans("core_protection_enabled", "✅ Core protection enabled"), false)
						if c.BotMessage != nil {
							_, _ = c.BotMessage.Delete()
						}
						return nil
					},
				},
				{
					Text:    m.getTrans("btn_no", "🚫 No"),
					Data:    "hset_coreprot_cancel",
					Handler: closeForm,
				},
			},
		},
	)
	return err
}

func getDisplayName(entity interface{}) string {
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

