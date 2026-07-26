package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

// forbiddenConstructorIDs maps a configurable method name to the TL constructor
// ID the client blocks. Only names listed here can be enforced.
var forbiddenConstructorIDs = map[string]uint32{
	"sendReaction":     3540875476,
	"joinChannel":      615851205,
	"importChatInvite": 1817183516,
}

// supportedForbiddenMethods lists the enforceable names in a stable order for
// error messages.
func supportedForbiddenMethods() []string {
	names := make([]string, 0, len(forbiddenConstructorIDs))
	for name := range forbiddenConstructorIDs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type APIProtection struct {
	client           *goroku.CustomTelegramClient
	db               *goroku.Database
	translator       *goroku.Translator
	forbiddenTypeIDs []uint32
}

var _ goroku.ModuleWithConfigSchema = (*APIProtection)(nil)

func (m *APIProtection) Name() string {
	return "APILimiter"
}

func (m *APIProtection) Strings() map[string]string {
	return map[string]string{
		"name":                   "APILimiter",
		"_cfg_time_sample":       "Time sample (in seconds) through which request count is measured",
		"_cfg_threshold":         "Threshold of requests to trigger protection",
		"_cfg_local_floodwait":   "Freeze userbot for this amount of time (in seconds) if request limit is exceeded",
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

// ConfigSchema is the M7 typed config surface for APILimiter (security).
func (m *APIProtection) ConfigSchema() []goroku.ConfigField {
	return []goroku.ConfigField{
		{Key: "time_sample", Type: "int", Default: 15, Validator: &goroku.IntegerValidator{}},
		{Key: "threshold", Type: "int", Default: 100, Validator: &goroku.IntegerValidator{}},
		{Key: "local_floodwait", Type: "int", Default: 30, Validator: &goroku.IntegerValidator{}},
		{Key: "forbidden_methods", Type: "series", Default: []any{"joinChannel", "importChatInvite"}, Validator: &goroku.SeriesValidator{}},
	}
}

func (m *APIProtection) ConfigReady(config map[string]any) error {
	return m.updateForbiddenMethods(config)
}

func (m *APIProtection) updateForbiddenMethods(config map[string]any) error {
	var forbidden []string
	if raw, ok := config["forbidden_methods"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, item := range arr {
				if str, ok := item.(string); ok {
					forbidden = append(forbidden, str)
				} else {
					return fmt.Errorf("APILimiter forbidden_methods contains type %T, want string", item)
				}
			}
		} else if arr, ok := raw.([]string); ok {
			forbidden = arr
		} else {
			return fmt.Errorf("APILimiter forbidden_methods has type %T, want string slice", raw)
		}
	} else {
		rawVal, err := m.db.Get("APILimiter", "forbidden_methods", []any{"joinChannel", "importChatInvite"})
		if err != nil {
			return err
		}
		if arr, ok := rawVal.([]any); ok {
			for _, item := range arr {
				if str, ok := item.(string); ok {
					forbidden = append(forbidden, str)
				} else {
					return fmt.Errorf("APILimiter forbidden_methods contains type %T, want string", item)
				}
			}
		} else if arr, ok := rawVal.([]string); ok {
			forbidden = arr
		} else {
			return fmt.Errorf("APILimiter forbidden_methods has type %T, want string slice", rawVal)
		}
	}

	var typeIDs []uint32
	var unknown []string
	for _, f := range forbidden {
		if id, ok := forbiddenConstructorIDs[f]; ok {
			typeIDs = append(typeIDs, id)
			continue
		}
		unknown = append(unknown, f)
	}
	if len(unknown) > 0 {
		// Silently dropping these used to leave the protection looking enabled
		// while blocking nothing.
		return fmt.Errorf("APILimiter forbidden_methods contains unsupported method(s) %s; supported: %s",
			strings.Join(unknown, ", "), strings.Join(supportedForbiddenMethods(), ", "))
	}

	m.forbiddenTypeIDs = typeIDs
	if m.client != nil {
		m.client.ForbiddenConstructors = typeIDs
	}
	return nil
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
	rawVal, err := m.db.Get("APILimiter", "disable_protection", true)
	if err != nil {
		return err
	}
	disable, ok := rawVal.(bool)
	if !ok {
		return fmt.Errorf("APILimiter disable_protection has type %T, want bool", rawVal)
	}
	newDisable := !disable
	if err := m.db.Set("APILimiter", "disable_protection", newDisable); err != nil {
		return err
	}

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
		_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
	}
	return nil
}

func (m *APIProtection) APIFWProtectionCmd(msg *goroku.Message) error {
	im := m.client.GorokuInline
	if im != nil && im.IsComplete() {
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
							rawVal, err := m.db.Get("APILimiter", "disable_protection", true)
							if err != nil {
								return err
							}
							disable, ok := rawVal.(bool)
							if !ok {
								return fmt.Errorf("APILimiter disable_protection has type %T, want bool", rawVal)
							}
							newDisable := !disable
							if err := m.db.Set("APILimiter", "disable_protection", newDisable); err != nil {
								return err
							}

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
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}
	seconds, err := strconv.Atoi(args)
	if err != nil || seconds < 0 {
		msg.Text = m.getTrans("args_invalid", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Invalid arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	m.client.RatelimitMu.Lock()
	m.client.BypassSuspendUntil = time.Now().Add(time.Duration(seconds) * time.Second)
	m.client.RatelimitMu.Unlock()

	template := m.getTrans("suspended_for", "<tg-emoji emoji-id=5458450833857322148>👌</tg-emoji> <b>API Flood Protection is disabled for {} seconds</b>")
	msg.Text = formatTrans(template, strconv.Itoa(seconds))
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
	}
	return nil
}

func (m *APIProtection) SuspendAPIProtectCmd(msg *goroku.Message) error {
	return m.SetfloodCmd(msg)
}
