package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

type Translate struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
	onlyText   bool
	provider   string
}

func (m *Translate) Name() string {
	return "Translate"
}

func (m *Translate) Strings() map[string]string {
	return map[string]string{
		"name": "Translate",
		"_cfg_only_text": "only translated text in .tr",
		"_cfg_provider": "Translation provider to use",
	}
}

func (m *Translate) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Translate) ClientReady() error { return nil }
func (m *Translate) OnUnload() error    { return nil }
func (m *Translate) OnDlmod() error     { return nil }

func (m *Translate) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"only_text": false,
		"provider":  "telegram",
	}
}

func (m *Translate) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["only_text"].(bool); ok {
		m.onlyText = val
	}
	if val, ok := config["provider"].(string); ok {
		m.provider = val
	}
	return nil
}

func (m *Translate) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"translate": m.TranslateCmd,
	}
}

func (m *Translate) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"translate": {
			Aliases: []string{"tr"},
		},
	}
}

func (m *Translate) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *Translate) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *Translate) TranslateCmd(msg *goroku.Message) error {
	rawText := utils.GetArgsRaw(msg.RawText)
	var lang string
	var text string
	hasText := false

	if rawText == "" {
		lang = m.getTrans("language", "en")
	} else {
		parts := strings.SplitN(rawText, " ", 2)
		firstWord := parts[0]
		if utf8.RuneCountInString(firstWord) != 2 {
			text = rawText
			lang = m.getTrans("language", "en")
			hasText = true
		} else {
			lang = firstWord
			if len(parts) > 1 {
				text = parts[1]
				hasText = true
			}
		}
	}

	if !hasText || text == "" {
		replyMsg, err := msg.GetReplyMessage()
		if err != nil || replyMsg == nil {
			_ = msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>No arguments provided</b>"))
			return nil
		}
		text = replyMsg.RawText
	}

	var trText string
	var translateErr error

	if m.provider == "telegram" {
		if msg.ReplyToMsgID != 0 && (!hasText || text == "") {
			trText, translateErr = m.client.Translate(msg.ChatID, int(msg.ReplyToMsgID), lang)
		} else {
			// Fallback to Google Translate for raw text
			trText, translateErr = translateGoogle(text, lang)
		}
	} else {
		trText, translateErr = translateGoogle(text, lang)
	}

	if translateErr != nil {
		_ = msg.Answer(m.getTrans("error", "<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>Unable to translate text</b>"))
		return nil
	}

	if m.onlyText {
		_ = msg.Answer(trText)
	} else {
		textTemplate := m.getTrans("translated_text", "<tg-emoji emoji-id=5787344001862471785>🔁</tg-emoji> Translated text:\n<blockquote>{tr_text}</blockquote>")
		formatted := strings.ReplaceAll(textTemplate, "{tr_text}", trText)
		_ = msg.Answer(formatted)
	}

	return nil
}

func translateGoogle(text string, lang string) (string, error) {
	apiURL := fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=%s&dt=t&q=%s", lang, url.QueryEscape(text))
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed []interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}

	if len(parsed) > 0 {
		if first, ok := parsed[0].([]interface{}); ok && len(first) > 0 {
			var translationParts []string
			for _, item := range first {
				if itemArr, ok := item.([]interface{}); ok && len(itemArr) > 0 {
					if transStr, ok := itemArr[0].(string); ok {
						translationParts = append(translationParts, transStr)
					}
				}
			}
			return strings.Join(translationParts, ""), nil
		}
	}
	return "", fmt.Errorf("empty translation results")
}
