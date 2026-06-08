package modules

import (
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type APIProtection struct {
	client           *goroku.CustomTelegramClient
	db               *goroku.Database
	translator       *goroku.Translator
	suspendUntil     time.Time
	forbiddenTypeIDs []int
}

func (m *APIProtection) Name() string {
	return "APILimiter"
}

func (m *APIProtection) Strings() map[string]string {
	return map[string]string{
		"name": "APILimiter",
		"_cfg_time_sample": "Time sample (in seconds) through which request count is measured",
		"_cfg_threshold": "Threshold of requests to trigger protection",
		"_cfg_local_floodwait": "Freeze userbot for this amount of time (in seconds) if request limit is exceeded",
		"_cfg_forbidden_methods": "Forbid specified methods from being executed throughout external modules",
	}
}

func (m *APIProtection) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	if len(m.forbiddenTypeIDs) > 0 {
		m.client.ForbiddenConstructors = m.forbiddenTypeIDs
	}
	return nil
}

func (m *APIProtection) getTrans(key, def string) string {
	return getTrans(m.translator, "api_protection", key, def)
}

func (m *APIProtection) ClientReady() error { return nil }
func (m *APIProtection) OnUnload() error    { return nil }
func (m *APIProtection) OnDlmod() error     { return nil }

func (m *APIProtection) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"time_sample":       15,
		"threshold":         100,
		"local_floodwait":   30,
		"forbidden_methods": []interface{}{"joinChannel", "importChatInvite"},
	}
}

func (m *APIProtection) ConfigReady(config map[string]interface{}) error {
	m.updateForbiddenMethods(config)
	return nil
}

func (m *APIProtection) updateForbiddenMethods(config map[string]interface{}) {
	var forbidden []string
	if raw, ok := config["forbidden_methods"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			for _, item := range arr {
				if str, ok := item.(string); ok {
					forbidden = append(forbidden, str)
				}
			}
		} else if arr, ok := raw.([]string); ok {
			forbidden = arr
		}
	} else {
		rawVal := m.db.Get("APILimiter", "forbidden_methods", []interface{}{"joinChannel", "importChatInvite"})
		if arr, ok := rawVal.([]interface{}); ok {
			for _, item := range arr {
				if str, ok := item.(string); ok {
					forbidden = append(forbidden, str)
				}
			}
		} else if arr, ok := rawVal.([]string); ok {
			forbidden = arr
		}
	}

	constructorMap := map[string]int{
		"sendReaction":     3540875476,
		"joinChannel":      615851205,
		"importChatInvite": 1817183516,
	}

	var typeIDs []int
	for _, f := range forbidden {
		if id, ok := constructorMap[f]; ok {
			typeIDs = append(typeIDs, id)
		}
	}

	m.forbiddenTypeIDs = typeIDs
	if m.client != nil {
		m.client.ForbiddenConstructors = typeIDs
	}
}

func (m *APIProtection) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"api_fw_protection":   m.APIFWProtectionCmd,
		"suspend_api_protect": m.SuspendAPIProtectCmd,
	}
}

func (m *APIProtection) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"api_fw_protection": {
			Aliases: []string{"antiflood"},
		},
		"suspend_api_protect": {
			Aliases: []string{"setflood"},
		},
	}
}

func (m *APIProtection) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *APIProtection) AntifloodCmd(msg *goroku.Message) error {
	rawVal := m.db.Get("APILimiter", "disable_protection", true)
	disable, ok := rawVal.(bool)
	if !ok {
		disable = true
	}
	newDisable := !disable
	m.db.Set("APILimiter", "disable_protection", newDisable)

	var statusKey string
	var statusDef string
	if newDisable {
		statusKey = "off"
		statusDef = "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>Protection disabled</b>"
	} else {
		statusKey = "on"
		statusDef = "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>Protection enabled</b>"
	}
	msg.Text = m.getTrans(statusKey, statusDef)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *APIProtection) APIFWProtectionCmd(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		_, err := im.Form(
			m.getTrans("u_sure", "<tg-emoji emoji-id=5312383351217201533>⚠️</tg-emoji> <b>Are you sure?</b>"),
			msg,
			[][]inline.Button{
				{
					{
						Text: m.getTrans("btn_no", "🚫 No"),
						Data: "api_fw_no",
						Handler: func(c inline.CallbackQuery) error {
							return closeForm(c)
						},
					},
					{
						Text: m.getTrans("btn_yes", "✅ Yes"),
						Data: "api_fw_yes",
						Handler: func(c inline.CallbackQuery) error {
							rawVal := m.db.Get("APILimiter", "disable_protection", true)
							disable, _ := rawVal.(bool)
							newDisable := !disable
							m.db.Set("APILimiter", "disable_protection", newDisable)

							var statusKey string
							var statusDef string
							if newDisable {
								statusKey = "off"
								statusDef = "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>Protection disabled</b>"
							} else {
								statusKey = "on"
								statusDef = "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>Protection enabled</b>"
							}
							text := m.getTrans(statusKey, statusDef)
							return c.InlineMessage.Edit(
								text,
								tgbotapi.InlineKeyboardMarkup{},
							)
						},
					},
				},
			},
		)
		return err
	}

	return m.AntifloodCmd(msg)
}

func (m *APIProtection) SetfloodCmd(msg *goroku.Message) error {
	args := utils.GetArgsRaw(msg.RawText)
	args = strings.TrimSpace(args)
	if args == "" {
		msg.Text = m.getTrans("args_invalid", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Invalid arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}
	seconds, err := strconv.Atoi(args)
	if err != nil || seconds < 0 {
		msg.Text = m.getTrans("args_invalid", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Invalid arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	m.client.RatelimitMu.Lock()
	m.client.BypassSuspendUntil = time.Now().Add(time.Duration(seconds) * time.Second)
	m.client.RatelimitMu.Unlock()

	template := m.getTrans("suspended_for", "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>API Flood Protection is disabled for {} seconds</b>")
	msg.Text = formatTrans(template, strconv.Itoa(seconds))
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *APIProtection) SuspendAPIProtectCmd(msg *goroku.Message) error {
	return m.SetfloodCmd(msg)
}
