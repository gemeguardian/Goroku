package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"sort"
	"strings"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

type TranslationsModule struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *TranslationsModule) Name() string {
	return "Translations"
}

func (m *TranslationsModule) Strings() map[string]string {
	return map[string]string{
		"name":               "Translations Module",
		"check_url":          "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>You need to specify valid url containing a langpack</b>",
		"pack_saved":         "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Translate pack saved!</b>",
		"check_pack":         "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Invalid pack format in url</b>",
		"incorrect_language": "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Incorrect language specified</b>",
		"lang_saved":         "{} <b>Language saved!</b>",
		"not_official":       "<tg-emoji emoji-id=5312383351217201533>⚠️</tg-emoji> <b>This language is not officially supported</b>",
	}
}


func (m *TranslationsModule) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *TranslationsModule) ClientReady() error { return nil }
func (m *TranslationsModule) OnUnload() error    { return nil }
func (m *TranslationsModule) OnDlmod() error     { return nil }

func (m *TranslationsModule) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"setlang":    m.SetLangCmd,
		"listlangs":  m.ListLangsCmd,
		"dllangpack": m.DlLangPackCmd,
	}
}

func (m *TranslationsModule) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func getFlag(lang string) string {
	emojiFlags := map[string]string{
		"🇬🇧": "<tg-emoji emoji-id=6323589145717376403>🇬🇧</tg-emoji>",
		"🇺🇿": "<tg-emoji emoji-id=5449829434334912605>🇺🇿</tg-emoji>",
		"🇷🇺": "<tg-emoji emoji-id=6323139226418284334>🇷🇺</tg-emoji>",
		"🇺🇦": "<tg-emoji emoji-id=5276140694891666474>🇺🇦</tg-emoji>",
		"🇮🇹": "<tg-emoji emoji-id=6323471399188957082>🇮🇹</tg-emoji>",
		"🇩🇪": "<tg-emoji emoji-id=6320817337033295141>🇩🇪</tg-emoji>",
		"🇪🇸": "<tg-emoji emoji-id=6323315062379382237>🇪🇸</tg-emoji>",
		"🇹🇷": "<tg-emoji emoji-id=6321003171678259486>🇹🇷</tg-emoji>",
		"🇰🇿": "<tg-emoji emoji-id=5228718354658769982>🇰🇿</tg-emoji>",
		"🥟": "<tg-emoji emoji-id=5382337996123020810>🥟</tg-emoji>",
		"🇯🇵": "<tg-emoji emoji-id=5456261908069885892>🇯🇵</tg-emoji>",
		"🇫🇷": "<tg-emoji emoji-id=5202132623060640759>🇫🇷</tg-emoji>",
		"🏴‍☠️": "<tg-emoji emoji-id=5386372293263892965>🏴‍☠️</tg-emoji>",
	}

	lang2country := map[string]string{
		"en":     "🇬🇧",
		"tt":     "🥟",
		"kz":     "🇰🇿",
		"ua":     "🇺🇦",
		"de":     "🇩🇪",
		"jp":     "🇯🇵",
		"fr":     "🇫🇷",
		"uz":     "🇺🇿",
		"ru":     "🇷🇺",
		"leet":   "🏴‍☠️",
		"uwu":    "🏴‍☠️",
		"uwu_ru": "🏴‍☠️",
		"tiktok": "🏴‍☠️",
		"neofit": "🏴‍☠️",
	}

	country, ok := lang2country[lang]
	if !ok {
		country = utils.GetLangFlag(lang)
	}
	if emoji, ok := emojiFlags[country]; ok {
		return emoji
	}
	return country
}

func (m *TranslationsModule) SetLangCmd(msg *goroku.Message) error {
	args := utils.GetArgs(msg.Text)
	if len(args) == 0 {
		return m.ChooseLanguage(msg, false)
	}

	var valid []string
	for _, arg := range args {
		arg = strings.ToLower(arg)
		isURL := strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://")
		if len(arg) == 2 || isURL {
			dup := false
			for _, v := range valid {
				if v == arg {
					dup = true
					break
				}
			}
			if !dup {
				valid = append(valid, arg)
			}
		} else {
			msg.Text = getTrans(m.translator, m.Name(), "incorrect_language", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Incorrect language specified</b>")
			if msg.Client != nil {
				msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
			}
			return nil
		}
	}

	langStr := strings.Join(valid, " ")
	m.db.Set("goroku.translations", "lang", langStr)
	m.translator.Init()

	var flags []string
	notOfficial := false
	for _, l := range valid {
		if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
			flags = append(flags, "<tg-emoji emoji-id=5433653135799228968>📁</tg-emoji>")
			notOfficial = true
		} else {
			flags = append(flags, getFlag(l))
			_, inSupported := goroku.SupportedLanguages[l]
			_, inMeme := goroku.MemeLanguages[l]
			if !inSupported && !inMeme {
				notOfficial = true
			}
		}
	}

	template := getTrans(m.translator, m.Name(), "lang_saved", "{} <b>Language saved!</b>")
	res := formatTrans(template, strings.Join(flags, ""))

	if notOfficial {
		res += "\n\n" + getTrans(m.translator, m.Name(), "not_official", "<tg-emoji emoji-id=5312383351217201533>⚠️</tg-emoji> <b>This language is not officially supported</b>")
	}

	msg.Text = res
	if msg.Client != nil {
		msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}

	return nil
}

func (m *TranslationsModule) ChooseLanguage(msg interface{}, isMeme bool) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		if msgObj, ok := msg.(*goroku.Message); ok {
			text := getTrans(m.translator, m.Name(), "incorrect_language", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Incorrect language specified</b>")
			_ = msgObj.Answer(text)
		}
		return nil
	}

	var langs map[string]string
	if !isMeme {
		langs = goroku.SupportedLanguages
	} else {
		langs = goroku.MemeLanguages
	}

	var buttons []inline.Button
	var langKeys []string
	for k := range langs {
		langKeys = append(langKeys, k)
	}
	sort.Strings(langKeys)

	for _, lang := range langKeys {
		text := langs[lang]
		l := lang
		buttons = append(buttons, inline.Button{
			Text: text,
			Handler: func(call inline.CallbackQuery) error {
				return m.ChangeLanguage(call, l)
			},
		})
	}

	var markup [][]inline.Button
	for i := 0; i < len(buttons); i += 2 {
		end := i + 2
		if end > len(buttons) {
			end = len(buttons)
		}
		markup = append(markup, buttons[i:end])
	}

	var toggleText string
	if isMeme {
		toggleText = getTrans(m.translator, m.Name(), "off_langs", "🏴‍☠️ Official LangPacks")
	} else {
		toggleText = getTrans(m.translator, m.Name(), "meme_langs", "🏴‍☠️ Meme LangPacks")
	}
	toggleBtn := inline.Button{
		Text: toggleText,
		Handler: func(call inline.CallbackQuery) error {
			return m.ChooseLanguage(call, !isMeme)
		},
	}
	markup = append(markup, []inline.Button{toggleBtn})

	text := getTrans(m.translator, m.Name(), "choose_language", "<tg-emoji emoji-id=5454219968948229067>🗽</tg-emoji> <b>Choose language</b>")

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup)
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func (m *TranslationsModule) ChangeLanguage(call inline.CallbackQuery, lang string) error {
	m.db.Set("goroku.translations", "lang", lang)
	m.translator.Init()

	flag := getFlag(lang)
	template := getTrans(m.translator, m.Name(), "lang_saved", "{} <b>Language saved!</b>")
	res := formatTrans(template, flag)

	return call.Edit(res, tgbotapi.InlineKeyboardMarkup{})
}

func (m *TranslationsModule) ListLangsCmd(msg *goroku.Message) error {
	currentLang := "en"
	if val, ok := m.db.Get("goroku.translations", "lang", "en").(string); ok {
		currentLang = val
	}

	var sb strings.Builder
	sb.WriteString("🗽 <b>Available Languages:</b>\n\n")

	sb.WriteString("<b>Official Languages:</b>\n")
	var supportedKeys []string
	for k := range goroku.SupportedLanguages {
		supportedKeys = append(supportedKeys, k)
	}
	sort.Strings(supportedKeys)

	for _, k := range supportedKeys {
		name := goroku.SupportedLanguages[k]
		active := ""
		if strings.Contains(currentLang, k) {
			active = " (active)"
		}
		sb.WriteString(fmt.Sprintf("• <code>%s</code> — %s%s\n", k, name, active))
	}

	sb.WriteString("\n<b>Meme Languages:</b>\n")
	var memeKeys []string
	for k := range goroku.MemeLanguages {
		memeKeys = append(memeKeys, k)
	}
	sort.Strings(memeKeys)

	for _, k := range memeKeys {
		name := goroku.MemeLanguages[k]
		active := ""
		if strings.Contains(currentLang, k) {
			active = " (active)"
		}
		sb.WriteString(fmt.Sprintf("• <code>%s</code> — %s%s\n", k, name, active))
	}

	sb.WriteString("\nSet language using: <code>.setlang &lt;lang_code&gt;</code>")

	msg.Text = sb.String()
	if msg.Client != nil {
		msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *TranslationsModule) DlLangPackCmd(msg *goroku.Message) error {
	args := utils.GetArgsRaw(msg.RawText)
	args = strings.TrimSpace(args)
	if args == "" || !utils.CheckURL(args) {
		msg.Text = getTrans(m.translator, m.Name(), "check_url", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>You need to specify valid url containing a langpack</b>")
		if msg.Client != nil {
			msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	var currentLang string
	if val, ok := m.db.Get("goroku.translations", "lang", "").(string); ok {
		currentLang = val
	}

	var cleanLangs []string
	for _, token := range strings.Fields(currentLang) {
		if !strings.HasPrefix(token, "http://") && !strings.HasPrefix(token, "https://") && !utils.CheckURL(token) {
			cleanLangs = append(cleanLangs, token)
		}
	}

	var newLang string
	if len(cleanLangs) > 0 {
		newLang = strings.Join(cleanLangs, " ") + " " + args
	} else {
		newLang = args
	}

	m.db.Set("goroku.translations", "lang", newLang)
	m.translator.Init()

	success := m.translator.HasRawData(args)
	if success {
		msg.Text = getTrans(m.translator, m.Name(), "pack_saved", "<tg-emoji emoji-id=5197474765387864959>👍</tg-emoji> <b>Translate pack saved!</b>")
	} else {
		msg.Text = getTrans(m.translator, m.Name(), "check_pack", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Invalid pack format in url</b>")
	}

	if msg.Client != nil {
		msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}

	return nil
}

