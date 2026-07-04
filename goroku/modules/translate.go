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
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
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
		"name":           "Translate",
		"_cfg_only_text": "only translated text in .tr",
		"_cfg_provider":  "Translation provider to use",
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

func (m *Translate) ConfigDefaults() map[string]any {
	return map[string]any{
		"only_text": false,
		"provider":  "telegram",
	}
}

func (m *Translate) ConfigReady(config map[string]any) error {
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

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func entitySpan(entity tg.MessageEntityClass) (int, int, bool) {
	switch e := entity.(type) {
	case *tg.MessageEntityBold:
		return e.Offset, e.Length, true
	case *tg.MessageEntityItalic:
		return e.Offset, e.Length, true
	case *tg.MessageEntityUnderline:
		return e.Offset, e.Length, true
	case *tg.MessageEntityStrike:
		return e.Offset, e.Length, true
	case *tg.MessageEntityCode:
		return e.Offset, e.Length, true
	case *tg.MessageEntityPre:
		return e.Offset, e.Length, true
	case *tg.MessageEntitySpoiler:
		return e.Offset, e.Length, true
	case *tg.MessageEntityBlockquote:
		return e.Offset, e.Length, true
	case *tg.MessageEntityTextURL:
		return e.Offset, e.Length, true
	case *tg.MessageEntityMentionName:
		return e.Offset, e.Length, true
	case *tg.MessageEntityCustomEmoji:
		return e.Offset, e.Length, true
	}
	return 0, 0, false
}

func cloneEntityWithSpan(entity tg.MessageEntityClass, offset, length int) tg.MessageEntityClass {
	switch e := entity.(type) {
	case *tg.MessageEntityBold:
		return &tg.MessageEntityBold{Offset: offset, Length: length}
	case *tg.MessageEntityItalic:
		return &tg.MessageEntityItalic{Offset: offset, Length: length}
	case *tg.MessageEntityUnderline:
		return &tg.MessageEntityUnderline{Offset: offset, Length: length}
	case *tg.MessageEntityStrike:
		return &tg.MessageEntityStrike{Offset: offset, Length: length}
	case *tg.MessageEntityCode:
		return &tg.MessageEntityCode{Offset: offset, Length: length}
	case *tg.MessageEntityPre:
		return &tg.MessageEntityPre{Offset: offset, Length: length, Language: e.Language}
	case *tg.MessageEntitySpoiler:
		return &tg.MessageEntitySpoiler{Offset: offset, Length: length}
	case *tg.MessageEntityBlockquote:
		return &tg.MessageEntityBlockquote{Offset: offset, Length: length, Collapsed: e.Collapsed}
	case *tg.MessageEntityTextURL:
		return &tg.MessageEntityTextURL{Offset: offset, Length: length, URL: e.URL}
	case *tg.MessageEntityMentionName:
		return &tg.MessageEntityMentionName{Offset: offset, Length: length, UserID: e.UserID}
	case *tg.MessageEntityCustomEmoji:
		return &tg.MessageEntityCustomEmoji{Offset: offset, Length: length, DocumentID: e.DocumentID}
	}
	return nil
}

func sliceMessageEntities(entities []tg.MessageEntityClass, payloadStart, payloadLen int) []tg.MessageEntityClass {
	payloadEnd := payloadStart + payloadLen
	var result []tg.MessageEntityClass
	for _, entity := range entities {
		offset, length, ok := entitySpan(entity)
		if !ok || length <= 0 {
			continue
		}
		entityStart := offset
		entityEnd := offset + length
		if entityEnd <= payloadStart || entityStart >= payloadEnd {
			continue
		}
		if entityStart < payloadStart {
			entityStart = payloadStart
		}
		if entityEnd > payloadEnd {
			entityEnd = payloadEnd
		}
		cloned := cloneEntityWithSpan(entity, entityStart-payloadStart, entityEnd-entityStart)
		if cloned != nil {
			result = append(result, cloned)
		}
	}
	return result
}

func describeEntities(entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(entities))
	for _, entity := range entities {
		offset, length, _ := entitySpan(entity)
		parts = append(parts, fmt.Sprintf("%T(offset=%d,length=%d)", entity, offset, length))
	}
	return strings.Join(parts, ", ")
}

func applyCustomEmojiFallback(htmlText, originalText string, entities []tg.MessageEntityClass) string {
	u16 := utf16.Encode([]rune(originalText))
	for _, entity := range entities {
		custom, ok := entity.(*tg.MessageEntityCustomEmoji)
		if !ok || custom.Length <= 0 || custom.Offset < 0 || custom.Offset+custom.Length > len(u16) {
			continue
		}
		visible := string(utf16.Decode(u16[custom.Offset : custom.Offset+custom.Length]))
		if visible == "" || !strings.Contains(htmlText, visible) {
			continue
		}
		tagged := fmt.Sprintf("<tg-emoji emoji-id=\"%d\">%s</tg-emoji>", custom.DocumentID, visible)
		htmlText = replaceFirstOutsideCustomEmojiTag(htmlText, visible, tagged)
	}
	return htmlText
}

func replaceFirstOutsideCustomEmojiTag(s, old, replacement string) string {
	start := 0
	for {
		idx := strings.Index(s[start:], old)
		if idx < 0 {
			return s
		}
		pos := start + idx
		prefix := s[:pos]
		lastOpen := strings.LastIndex(prefix, "<tg-emoji")
		lastClose := strings.LastIndex(prefix, "</tg-emoji>")
		if lastOpen <= lastClose {
			return s[:pos] + replacement + s[pos+len(old):]
		}
		start = pos + len(old)
	}
}

func (m *Translate) TranslateCmd(msg *goroku.Message) error {
	rawText := utils.GetArgsRaw(msg.RawText)
	var lang string
	var text string
	var entities []tg.MessageEntityClass
	replyMsgID := 0
	payloadStartByte := -1
	hasText := false
	if idx := strings.Index(msg.RawText, " "); idx >= 0 {
		payloadStartByte = idx + 1
	}
	L().Debug("start raw={0} rawArgs={1} msgEntities={2} [{3}]", zap.Any("arg0", msg.RawText), zap.Any("arg1", rawText), zap.Any("arg2", len(msg.Entities)), zap.String("entities", describeEntities(msg.Entities)))

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
				if payloadStartByte >= 0 {
					payloadStartByte += len(firstWord) + 1
				}
				hasText = true
			}
		}
	}
	if hasText && payloadStartByte >= 0 {
		payloadStart := utf16Len(msg.RawText[:payloadStartByte])
		payloadLen := utf16Len(text)
		entities = sliceMessageEntities(msg.Entities, payloadStart, payloadLen)
		L().Debug("inline payloadStartByte={0} payloadStartUTF16={1} payloadLenUTF16={2} text={3} slicedEntities={4} [{5}]", zap.Any("arg0", payloadStartByte), zap.Any("arg1", payloadStart), zap.Any("arg2", payloadLen), zap.Any("arg3", text), zap.Any("arg4", len(entities)), zap.String("entities", describeEntities(entities)))
	}

	if !hasText || text == "" {
		replyMsg, err := msg.GetReplyMessage()
		if err != nil || replyMsg == nil {
			L().Debug("no text and no reply: err={0} replyNil={1}", zap.Any("arg0", err), zap.Any("arg1", replyMsg == nil))
			_ = msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>No arguments provided</b>"))
			return nil
		}
		text = replyMsg.RawText
		entities = replyMsg.Entities
		replyMsgID = int(msg.ReplyToMsgID)
		L().Debug("reply text={0} replyEntities={1} [{2}]", zap.Any("arg0", text), zap.Any("arg1", len(entities)), zap.String("entities", describeEntities(entities)))
	}

	var trText string
	var translateErr error
	translateInput := text
	if len(entities) > 0 {
		translateInput = goroku.EntitiesToHTML(text, entities)
	}

	if m.provider == "telegram" {
		if replyMsgID != 0 && !hasText {
			L().Debug("calling telegram provider by message lang={0} msgID={1}", zap.Any("arg0", lang), zap.Any("arg1", replyMsgID))
			trText, translateErr = m.client.Translate(goroku.ChatRefID(msg.ChatID), replyMsgID, lang)
		} else {
			L().Debug("calling telegram provider lang={0} html={1}", zap.Any("arg0", lang), zap.Any("arg1", translateInput))
			trText, translateErr = m.client.TranslateText(goroku.ChatRefID(msg.ChatID), translateInput, nil, lang)
		}
	} else {
		L().Debug("calling google provider lang={0} html={1}", zap.Any("arg0", lang), zap.Any("arg1", translateInput))
		trText, translateErr = translateGoogle(translateInput, lang)
	}

	if translateErr != nil {
		L().Debug("translate failed: {0}", zap.Any("arg0", translateErr))
		_ = msg.Answer(m.getTrans("error", "<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>Unable to translate text</b>"))
		return nil
	}
	trText = applyCustomEmojiFallback(trText, text, entities)
	L().Debug("translated html={0}", zap.Any("arg0", trText))

	if m.onlyText {
		L().Debug("answer onlyText html={0}", zap.Any("arg0", trText))
		_ = msg.Answer(trText)
	} else {
		textTemplate := m.getTrans("translated_text", "<tg-emoji emoji-id=\"5972247240217988372\">🅰</tg-emoji> Translated text:\n<blockquote>{tr_text}</blockquote>")
		formatted := strings.ReplaceAll(textTemplate, "{tr_text}", trText)
		L().Debug("answer formatted html={0}", zap.Any("arg0", formatted))
		_ = msg.Answer(formatted)
	}

	return nil
}

func translateGoogle(text string, lang string) (string, error) {
	apiURL := fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=%s&dt=t&q=%s", lang, url.QueryEscape(text))
	resp, err := http.Get(apiURL) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed []any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}

	if len(parsed) > 0 {
		if first, ok := parsed[0].([]any); ok && len(first) > 0 {
			var translationParts []string
			for _, item := range first {
				if itemArr, ok := item.([]any); ok && len(itemArr) > 0 {
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
