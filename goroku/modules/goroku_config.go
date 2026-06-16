package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	stdhtml "html"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GorokuConfig struct {
	client               *goroku.CustomTelegramClient
	db                   *goroku.Database
	translator           *goroku.Translator
	cfgEmoji             string
	startEmoji           string
	listEmoji            string
	validationErrorEmoji string
	detectiveEmoji       string
	infoEmoji            string
}

var configEmojiRe = regexp.MustCompile(`(?i)<tg-emoji\s+emoji-id=["']?5341715473882955310["']?>(?:⚙️|🪐)</tg-emoji>`)
var tgEmojiTagRe = regexp.MustCompile(`(?is)</?tg-emoji\b[^>]*>`)

func (m *GorokuConfig) Name() string {
	return "GorokuConfig"
}

func (m *GorokuConfig) Strings() map[string]string {
	return map[string]string{
		"name":                        "Goroku Config Module",
		"args":                        "🚫 <b>You specified incorrect args</b>",
		"no_mod":                      "🚫 <b>Module doesn't exist</b>",
		"no_option":                   "🚫 <b>Configuration option doesn't exist</b>",
		"option_saved":                "⚙️ <b>Option</b> <code>%s</code> <b>of module</b> <code>%s</code><b> saved!</b>\n<b>Current:</b> <code>%s</code>",
		"option_reset":                "♻️ <b>Option</b> <code>%s</code> <b>of module</b> <code>%s</code> <b>has been reset</b>",
		"header_modules":              "⚙️ <b>Goroku Userbot Configuration</b>\n\nChoose a module to configure using <code>.config [module_name]</code> or set directly via <code>.fcfg [module] [key] [value]</code> / reset via <code>.dcfg [module] [key]</code>:\n\n",
		"module_info":                 "⚙️ <b>Configuration of module</b> <code>%s</code>:\n\n",
		"builtin":                     "🛰 Встроенные",
		"external":                    "🛸 Внешние",
		"_cfg_cfg_emoji":              "Изменить эмодзи после открытия конфига",
		"_cfg_list_emoji":             "Эмодзи элемента списка в конфиге",
		"_cfg_start_emoji":            "Эмодзи, отображаемый при открытии конфига. Премиум-теги эмодзи вроде &lt;tg-emoji emoji-id=\"...\"&gt; здесь использовать нельзя — они будут показаны как текст; остальные HTML-теги работают.",
		"_cfg_validation_error_emoji": "Эмодзи ошибки валидации",
		"_cfg_detective_emoji":        "Эмодзи детектива (🕵️) в подсказках типов",
		"_cfg_info_emoji":             "Эмодзи информации (ℹ️) в заголовке",
	}
}

func (m *GorokuConfig) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	m.cfgEmoji = "<tg-emoji emoji-id=5350628475914971096>🍃</tg-emoji>"
	m.startEmoji = "🍃"
	m.listEmoji = "<tg-emoji emoji-id=5278497648389691517>▫️</tg-emoji>"
	m.validationErrorEmoji = "<tg-emoji emoji-id=\"4918014360267260850\">⛔️</tg-emoji>"
	m.detectiveEmoji = "<tg-emoji emoji-id=\"5350830008665400761\">🕵️</tg-emoji>"
	m.infoEmoji = "<tg-emoji emoji-id=\"5247029067256987229\">ℹ️</tg-emoji>"
	return nil
}

func (m *GorokuConfig) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"cfg_emoji":              "<tg-emoji emoji-id=5350628475914971096>🍃</tg-emoji>",
		"start_emoji":            "🍃",
		"list_emoji":             "<tg-emoji emoji-id=5278497648389691517>▫️</tg-emoji>",
		"validation_error_emoji": "<tg-emoji emoji-id=\"4918014360267260850\">⛔️</tg-emoji>",
		"detective_emoji":        "<tg-emoji emoji-id=\"5350830008665400761\">🕵️</tg-emoji>",
		"info_emoji":             "<tg-emoji emoji-id=\"5247029067256987229\">ℹ️</tg-emoji>",
	}
}

func (m *GorokuConfig) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["cfg_emoji"].(string); ok {
		m.cfgEmoji = val
	}
	if val, ok := config["start_emoji"].(string); ok {
		m.startEmoji = val
	}
	if val, ok := config["list_emoji"].(string); ok {
		m.listEmoji = val
	}
	if val, ok := config["validation_error_emoji"].(string); ok {
		m.validationErrorEmoji = val
	}
	if val, ok := config["detective_emoji"].(string); ok {
		m.detectiveEmoji = val
	}
	if val, ok := config["info_emoji"].(string); ok {
		m.infoEmoji = val
	}
	return nil
}

func (m *GorokuConfig) getValidationErrorEmoji() string {
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "validation_error_emoji", "").(string); ok && dbEmoji != "" {
			m.validationErrorEmoji = dbEmoji
		}
	}
	return m.validationErrorEmoji
}

func (m *GorokuConfig) getDetectiveEmoji() string {
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "detective_emoji", "").(string); ok && dbEmoji != "" {
			m.detectiveEmoji = dbEmoji
		}
	}
	return m.detectiveEmoji
}

func (m *GorokuConfig) getInfoEmoji() string {
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "info_emoji", "").(string); ok && dbEmoji != "" {
			m.infoEmoji = dbEmoji
		}
	}
	return m.infoEmoji
}

func (m *GorokuConfig) ClientReady() error { return nil }
func (m *GorokuConfig) OnUnload() error    { return nil }
func (m *GorokuConfig) OnDlmod() error     { return nil }

func (m *GorokuConfig) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"config":  m.ConfigCmd,
		"fconfig": m.FConfigCmd,
		"dconfig": m.DConfigCmd,
	}
}

func (m *GorokuConfig) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"config": {
			Aliases: []string{"cfg"},
		},
		"fconfig": {
			Aliases: []string{"fcfg"},
		},
		"dconfig": {
			Aliases: []string{"dcfg"},
		},
	}
}

func (m *GorokuConfig) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuConfig) getTrans(key, def string) string {
	val := getTrans(m.translator, m.Name(), key, def)
	// Apply custom emoji replacement
	emoji := m.cfgEmoji
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "cfg_emoji", "").(string); ok && dbEmoji != "" {
			emoji = dbEmoji
			m.cfgEmoji = dbEmoji
		}
	}
	if emoji == "" {
		emoji = "<tg-emoji emoji-id=5350628475914971096>🍃</tg-emoji>"
	}
	val = configEmojiRe.ReplaceAllString(val, emoji)
	val = strings.ReplaceAll(val, "⚙️", emoji)
	val = strings.ReplaceAll(val, "🪐", emoji)
	return val
}

func (m *GorokuConfig) getListEmoji() string {
	emoji := m.listEmoji
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "list_emoji", "").(string); ok && dbEmoji != "" {
			emoji = dbEmoji
			m.listEmoji = dbEmoji
		}
	}
	if emoji == "" {
		emoji = "<tg-emoji emoji-id=5278497648389691517>▫️</tg-emoji>"
	}
	return emoji
}

func (m *GorokuConfig) getStartText() string {
	emoji := m.startEmoji
	if m.db != nil {
		if dbEmoji, ok := m.db.Get(m.Name(), "start_emoji", "").(string); ok && dbEmoji != "" {
			emoji = dbEmoji
			m.startEmoji = dbEmoji
		}
	}
	if emoji == "" {
		emoji = "🍃"
	}
	emoji = tgEmojiTagRe.ReplaceAllStringFunc(emoji, stdhtml.EscapeString)
	return emoji
}

func (m *GorokuConfig) reloadModule(modName string) {
	if loader, ok := m.client.Loader.(*goroku.Modules); ok && loader != nil {
		loader.ReloadModuleConfig(modName)
	}
}

func (m *GorokuConfig) optionExists(mod goroku.Module, option string) bool {
	optLower := strings.ToLower(option)
	// Check hardcoded schemas first
	if modSchemas, exists := schemas[strings.ToLower(mod.Name())]; exists {
		if _, exists := modSchemas[optLower]; exists {
			return true
		}
	}
	// Check ModuleWithConfigValidators
	if withValidators, ok := mod.(goroku.ModuleWithConfigValidators); ok {
		for k := range withValidators.ConfigValidators() {
			if strings.ToLower(k) == optLower {
				return true
			}
		}
	}
	// Check dynamic ConfigDefaults
	if withConfig, ok := mod.(goroku.ModuleWithConfig); ok {
		for k := range withConfig.ConfigDefaults() {
			if strings.ToLower(k) == optLower {
				return true
			}
		}
	}
	return false
}

func (m *GorokuConfig) makeButton(text string, handler func(inline.CallbackQuery) error) inline.Button {
	rand.Seed(time.Now().UnixNano())
	return inline.Button{
		Text:    text,
		Data:    fmt.Sprintf("cfg_%d_%d", time.Now().UnixNano(), rand.Int63()),
		Handler: handler,
	}
}

func (m *GorokuConfig) makeDangerButton(text string, handler func(inline.CallbackQuery) error) inline.Button {
	btn := m.makeButton(text, handler)
	btn.Style = "danger"
	return btn
}

func (m *GorokuConfig) makeBackButton(handler func(inline.CallbackQuery) error) inline.Button {
	btn := m.makeButton(m.getTrans("back_btn", "👈 Back"), handler)
	btn.Style = "primary"
	return btn
}

func (m *GorokuConfig) makeCloseButton() inline.Button {
	return inline.Button{
		Text:  m.getTrans("close_btn", "❌ Close"),
		Style: "danger",
		Handler: func(call inline.CallbackQuery) error {
			return closeForm(call)
		},
	}
}

func unwrapValidator(v goroku.Validator) goroku.Validator {
	if hidden, ok := v.(*goroku.HiddenValidator); ok {
		return unwrapValidator(hidden.Inner)
	}
	return v
}

func prepValue(val interface{}) string {
	if val == nil {
		return "<code>None</code>"
	}
	switch v := val.(type) {
	case string:
		return fmt.Sprintf("<code>%s</code>", utils.EscapeHTML(strings.TrimSpace(v)))
	case []interface{}:
		if len(v) == 0 {
			return "<code>[]</code>"
		}
		var sb strings.Builder
		sb.WriteString("<code>[</code>\n    ")
		for i, item := range v {
			if i > 0 {
				sb.WriteString("\n    ")
			}
			sb.WriteString(fmt.Sprintf("<code>%s</code>", utils.EscapeHTML(fmt.Sprintf("%v", item))))
		}
		sb.WriteString("\n<code>]</code>")
		return sb.String()
	case []string:
		if len(v) == 0 {
			return "<code>[]</code>"
		}
		var sb strings.Builder
		sb.WriteString("<code>[</code>\n    ")
		for i, item := range v {
			if i > 0 {
				sb.WriteString("\n    ")
			}
			sb.WriteString(fmt.Sprintf("<code>%s</code>", utils.EscapeHTML(item)))
		}
		sb.WriteString("\n<code>]</code>")
		return sb.String()
	default:
		return fmt.Sprintf("<code>%v</code>", utils.EscapeHTML(fmt.Sprintf("%v", val)))
	}
}

func getDefaultValue(modName, key string) interface{} {
	modNameLower := strings.ToLower(modName)
	keyLower := strings.ToLower(key)

	switch modNameLower {
	case "updater":
		switch keyLower {
		case "disable_notifications":
			return false
		case "autoupdate":
			return false
		case "ignore_permanent":
			return ""
		case "announcement":
			return ""
		}
	case "translations":
		if keyLower == "lang" {
			return "en"
		}
	case "settings":
		if keyLower == "aliases" {
			return []interface{}{}
		}
	case "goroku.main":
		switch keyLower {
		case "command_prefix":
			return "."
		case "no_nickname":
			return false
		case "grep":
			return false
		case "inlinelogs":
			return false
		case "suggest_subscribe":
			return false
		}
	case "goroku.inline":
		switch keyLower {
		case "custom_bot":
			return ""
		case "bot_token":
			return ""
		}
	case "gorokuinfo":
		switch keyLower {
		case "custom_message":
			return ""
		case "banner_url":
			return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/goroku_info.png"
		case "ping_emoji":
			return "🪐"
		case "quote_media":
			return false
		case "invert_media":
			return false
		case "show_goroku":
			return true
		}
	case "tester":
		switch keyLower {
		case "force_send_all":
			return false
		case "tglog_level":
			return "ALL"
		case "ignore_common":
			return false
		case "disable_internet_warn":
			return false
		case "custom_message":
			return ""
		case "banner_url":
			return ""
		case "quote_media":
			return false
		case "invert_media":
			return false
		case "ping_emoji":
			return "🪐"
		case "hint":
			return ""
		}
	}
	return ""
}

func (m *GorokuConfig) getDefaultValue(modName, key string) interface{} {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if ok && loader != nil {
		if mod := loader.LookupByName(modName); mod != nil {
			if withConfig, ok := mod.(goroku.ModuleWithConfig); ok {
				for cfgKey, value := range withConfig.ConfigDefaults() {
					if strings.EqualFold(cfgKey, key) {
						return value
					}
				}
			}
		}
	}
	return getDefaultValue(modName, key)
}

func (m *GorokuConfig) getOptionValue(modName, key string) interface{} {
	val := m.db.Get(modName, key, nil)
	if val == nil {
		val = m.db.Get(strings.ToLower(modName), strings.ToLower(key), nil)
	}
	if val == nil {
		val = m.getDefaultValue(modName, key)
	}
	return val
}

func (m *GorokuConfig) getOptionDoc(modName, key string) string {
	// 1. Try _cfg_doc_key first (common style)
	searchKey := fmt.Sprintf("_cfg_doc_%s", key)
	doc := getTrans(m.translator, modName, searchKey, "")

	// 2. Try _cfg_key
	if doc == "" || doc == "Unknown string" {
		searchKey = fmt.Sprintf("_cfg_%s", key)
		doc = getTrans(m.translator, modName, searchKey, "")
	}

	// 3. Try custom mappings for GorokuInfo
	if (doc == "" || doc == "Unknown string") && strings.EqualFold(modName, "GorokuInfo") {
		if key == "custom_message" {
			doc = getTrans(m.translator, modName, "_cfg_cst_msg", "")
		} else if key == "banner_url" {
			doc = getTrans(m.translator, modName, "_cfg_banner", "")
		} else if key == "ping_emoji" {
			doc = getTrans(m.translator, modName, "ping_emoji", "")
		}
	}

	// 4. Try fallback to direct lookup in target module's Strings()
	if doc == "" || doc == "Unknown string" {
		loader, ok := m.client.Loader.(*goroku.Modules)
		if ok && loader != nil {
			targetMod := loader.LookupByName(modName)
			if targetMod != nil {
				// Try _cfg_doc_key
				if val, exists := targetMod.Strings()[fmt.Sprintf("_cfg_doc_%s", key)]; exists {
					return val
				}
				// Try _cfg_key
				if val, exists := targetMod.Strings()[fmt.Sprintf("_cfg_%s", key)]; exists {
					return val
				}
				// Try direct key
				if val, exists := targetMod.Strings()[key]; exists {
					return val
				}
			}
		}
		doc = "No description available."
	}
	return doc
}

func (m *GorokuConfig) ConfigCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		if rawArgs != "" {
			parts := strings.Fields(rawArgs)
			loader, ok := m.client.Loader.(*goroku.Modules)
			if ok && loader != nil {
				targetModule := loader.LookupByName(parts[0])
				if targetModule != nil {
					if _, hasConfig := targetModule.(goroku.ModuleWithConfig); !hasConfig {
						msg.Text = "🚫 <b>This module has no configuration options</b>"
						msg.Answer(msg.Text)
						return nil
					}
					if len(parts) >= 2 {
						return m.ConfigureOption(msg, targetModule.Name(), parts[1], false, "")
					}
					return m.ConfigureModule(msg, targetModule.Name(), "")
				}
			}
		}
		return m.ChooseCategory(msg)
	}
	return m.textConfig(msg)
}

func (m *GorokuConfig) ChooseCategory(msg interface{}) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	presetFolders := make(map[string]interface{})
	foldersVal := m.db.Get("presets", "folders", nil)
	if foldersVal != nil {
		if bytes, err := json.Marshal(foldersVal); err == nil {
			json.Unmarshal(bytes, &presetFolders)
		}
	}

	var folderBtns []inline.Button
	for folderName := range presetFolders {
		fName := folderName
		folderBtns = append(folderBtns, m.makeButton("📁 "+fName, func(call inline.CallbackQuery) error {
			return m.ChooseFolderModuleList(call, fName)
		}))
	}
	sort.Slice(folderBtns, func(i, j int) bool {
		return folderBtns[i].Text < folderBtns[j].Text
	})

	var hasExternal bool
	loader, ok := m.client.Loader.(*goroku.Modules)
	if ok && loader != nil {
		for _, mod := range loader.GetModules() {
			nameLower := strings.ToLower(mod.Name())
			if !builtInModules[nameLower] {
				if _, hasConfig := mod.(goroku.ModuleWithConfig); hasConfig {
					hasExternal = true
					break
				}
			}
		}
	}

	var catRow []inline.Button
	builtinBtn := m.makeButton(m.getTrans("builtin", "🛰 Built-in"), func(call inline.CallbackQuery) error {
		return m.ChooseModuleList(call, true, 0)
	})
	catRow = append(catRow, builtinBtn)
	if hasExternal {
		externalBtn := m.makeButton(m.getTrans("external", "🛸 External"), func(call inline.CallbackQuery) error {
			return m.ChooseModuleList(call, false, 0)
		})
		catRow = append(catRow, externalBtn)
	}

	markup := [][]inline.Button{catRow}

	for i := 0; i < len(folderBtns); i += 2 {
		end := i + 2
		if end > len(folderBtns) {
			end = len(folderBtns)
		}
		markup = append(markup, folderBtns[i:end])
	}

	markup = append(markup, []inline.Button{
		m.makeCloseButton(),
	})

	text := m.getTrans("choose_core", "⚙️ <b>Choose a category</b>")

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup, inline.WithStartText(m.getStartText()))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

var builtInModules = map[string]bool{
	"apilimiter":           true,
	"eval":                 true,
	"help":                 true,
	"gorokubackup":         true,
	"gorokuconfig":         true,
	"gorokuinfo":           true,
	"gorokupluginsecurity": true,
	"gorokusecurity":       true,
	"gorokusettings":       true,
	"gorokuweb":            true,
	"inlinestuff":          true,
	"loader":               true,
	"presets":              true,
	"quickstart":           true,
	"settings":             true,
	"tester":               true,
	"terminal":             true,
	"translate":            true,
	"translator":           true,
	"translations":         true,
	"updater":              true,
}

func (m *GorokuConfig) ChooseModuleList(msg interface{}, isBuiltin bool, page int) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return fmt.Errorf("modules registry not found")
	}

	var modulesList []string
	for _, mod := range loader.GetModules() {
		name := mod.Name()
		nameLower := strings.ToLower(name)
		isBuiltinMod := builtInModules[nameLower]
		if isBuiltin == isBuiltinMod {
			if _, hasConfig := mod.(goroku.ModuleWithConfig); hasConfig {
				modulesList = append(modulesList, name)
			}
		}
	}
	sort.Strings(modulesList)

	const itemsPerPage = 15
	totalPages := (len(modulesList) + itemsPerPage - 1) / itemsPerPage
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * itemsPerPage
	endIdx := startIdx + itemsPerPage
	if endIdx > len(modulesList) {
		endIdx = len(modulesList)
	}

	var buttons []inline.Button
	for _, modName := range modulesList[startIdx:endIdx] {
		name := modName
		buttons = append(buttons, m.makeButton(name, func(call inline.CallbackQuery) error {
			return m.ConfigureModule(call, name, "")
		}))
	}

	var markup [][]inline.Button
	for i := 0; i < len(buttons); i += 3 {
		end := i + 3
		if end > len(buttons) {
			end = len(buttons)
		}
		markup = append(markup, buttons[i:end])
	}

	if totalPages > 1 {
		var pagRow []inline.Button
		if page > 0 {
			pagRow = append(pagRow, m.makeButton("◀️", func(call inline.CallbackQuery) error {
				return m.ChooseModuleList(call, isBuiltin, page-1)
			}))
		}
		pagRow = append(pagRow, inline.Button{Text: fmt.Sprintf("%d/%d", page+1, totalPages), Data: "noop"})
		if page < totalPages-1 {
			pagRow = append(pagRow, m.makeButton("▶️", func(call inline.CallbackQuery) error {
				return m.ChooseModuleList(call, isBuiltin, page+1)
			}))
		}
		markup = append(markup, pagRow)
	}

	markup = append(markup, []inline.Button{
		m.makeBackButton(func(call inline.CallbackQuery) error {
			return m.ChooseCategory(call)
		}),
		m.makeCloseButton(),
	})

	textKey := "configure"
	if !isBuiltin {
		textKey = "configure_lib"
	}
	text := m.getTrans(textKey, "⚙️ <b>Choose a module to configure</b>")

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup, inline.WithStartText(m.getStartText()))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func (m *GorokuConfig) ChooseFolderList(msg interface{}) error {
	return m.ChooseCategory(msg)
}

func (m *GorokuConfig) ChooseFolderModuleList(msg interface{}, folderName string) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	presetFolders := make(map[string]interface{})
	foldersVal := m.db.Get("presets", "folders", nil)
	if foldersVal != nil {
		if bytes, err := json.Marshal(foldersVal); err == nil {
			json.Unmarshal(bytes, &presetFolders)
		}
	}

	var modNames []interface{}
	if list, exists := presetFolders[folderName]; exists {
		if arr, ok := list.([]interface{}); ok {
			modNames = arr
		}
	}

	var btns []inline.Button
	var textParts []string
	for _, rawMod := range modNames {
		modStr := fmt.Sprintf("%v", rawMod)
		textParts = append(textParts, fmt.Sprintf("%s <b>%s</b>", m.getListEmoji(), utils.EscapeHTML(modStr)))
		btns = append(btns, m.makeButton(modStr, func(call inline.CallbackQuery) error {
			return m.ConfigureModule(call, modStr, folderName)
		}))
	}

	titleTrans := m.getTrans("configuring_folder", "📁 <b>Choose config option for folder</b> <code>{0}</code>\n\n<b>Current options:</b>\n\n{1}")
	text := formatTrans(titleTrans, utils.EscapeHTML(folderName), strings.Join(textParts, "\n"))

	var markup [][]inline.Button
	for i := 0; i < len(btns); i += 2 {
		end := i + 2
		if end > len(btns) {
			end = len(btns)
		}
		markup = append(markup, btns[i:end])
	}

	markup = append(markup, []inline.Button{
		m.makeBackButton(func(call inline.CallbackQuery) error {
			return m.ChooseCategory(call)
		}),
		m.makeCloseButton(),
	})

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup, inline.WithStartText(m.getStartText()))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func (m *GorokuConfig) ConfigureModule(msg interface{}, modName string, fromFolder string) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return fmt.Errorf("modules registry not found")
	}

	targetModule := loader.LookupByName(modName)
	if targetModule == nil {
		return fmt.Errorf("module not found")
	}

	optionsSet := make(map[string]bool)
	if modSchemas, exists := schemas[strings.ToLower(modName)]; exists {
		for k := range modSchemas {
			optionsSet[k] = true
		}
	}
	if withValidators, ok := targetModule.(goroku.ModuleWithConfigValidators); ok {
		for k := range withValidators.ConfigValidators() {
			optionsSet[strings.ToLower(k)] = true
		}
	}
	if withConfig, ok := targetModule.(goroku.ModuleWithConfig); ok {
		for k := range withConfig.ConfigDefaults() {
			optionsSet[strings.ToLower(k)] = true
		}
	}
	dbData := m.db.GetAll()
	for _, owner := range []string{targetModule.Name(), strings.ToLower(targetModule.Name())} {
		if innerMap, exists := dbData[owner]; exists {
			for k := range innerMap {
				optionsSet[strings.ToLower(k)] = true
			}
		}
	}

	var optionsList []string
	for k := range optionsSet {
		optionsList = append(optionsList, k)
	}
	sort.Strings(optionsList)

	var sb strings.Builder
	titleTrans := m.getTrans("configuring_mod", "⚙️ <b>Choose config option for mod</b> <code>{0}</code>\n\n<b>Current options:</b>\n\n{1}")

	var btns []inline.Button
	for _, optName := range optionsList {
		opt := optName
		curVal := m.getOptionValue(targetModule.Name(), opt)
		curValStr := fmt.Sprintf("%v", curVal)
		if m.isOptionHidden(targetModule.Name(), opt) {
			curValStr = "••••••••"
		} else if len(curValStr) > 40 {
			curValStr = curValStr[:37] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s <code>%s</code>: <b>%s</b>\n", m.getListEmoji(), opt, utils.EscapeHTML(curValStr)))

		btns = append(btns, m.makeButton(opt, func(call inline.CallbackQuery) error {
			return m.ConfigureOption(call, targetModule.Name(), opt, false, fromFolder)
		}))
	}

	if len(optionsList) == 0 {
		sb.WriteString("<i>No configuration options</i>")
	}

	text := formatTrans(titleTrans, targetModule.Name(), sb.String())

	var markup [][]inline.Button
	for i := 0; i < len(btns); i += 2 {
		end := i + 2
		if end > len(btns) {
			end = len(btns)
		}
		markup = append(markup, btns[i:end])
	}

	backHandler := func(call inline.CallbackQuery) error {
		if fromFolder != "" {
			return m.ChooseFolderModuleList(call, fromFolder)
		}
		isBuiltin := builtInModules[strings.ToLower(targetModule.Name())]
		return m.ChooseModuleList(call, isBuiltin, 0)
	}

	markup = append(markup, []inline.Button{
		m.makeBackButton(backHandler),
		m.makeCloseButton(),
	})

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup, inline.WithStartText(m.getStartText()))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func (m *GorokuConfig) getValidatorDocName(v goroku.Validator) string {
	if v == nil {
		return ""
	}
	switch v.(type) {
	case *goroku.BooleanValidator:
		return m.getTrans("validator_bool", "boolean")
	case *goroku.IntegerValidator:
		return m.getTrans("validator_int", "integer")
	case *goroku.StringValidator:
		return m.getTrans("validator_string", "string")
	case *goroku.FloatValidator:
		return m.getTrans("validator_float", "float")
	case *goroku.ChoiceValidator:
		return m.getTrans("validator_choice", "choice")
	case *goroku.SeriesValidator:
		return m.getTrans("validator_series", "series")
	}
	return "value"
}

func (m *GorokuConfig) ConfigureOption(msg interface{}, modName, optionName string, forceHidden bool, fromFolder string, errMsgs ...string) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	doc := m.getOptionDoc(modName, optionName)
	defVal := m.getDefaultValue(modName, optionName)
	curVal := m.getOptionValue(modName, optionName)

	validator := m.getValidator(modName, optionName)

	isBool := false
	isChoice := false
	isSeries := false
	isHidden := false

	var unwrapped goroku.Validator
	if validator != nil {
		unwrapped = unwrapValidator(validator)
		if _, ok := validator.(*goroku.HiddenValidator); ok {
			isHidden = true
		}
		switch unwrapped.(type) {
		case *goroku.BooleanValidator:
			isBool = true
		case *goroku.ChoiceValidator:
			isChoice = true
		case *goroku.SeriesValidator:
			isSeries = true
		}
	}
	if validator == nil {
		switch defVal.(type) {
		case bool:
			isBool = true
			unwrapped = &goroku.BooleanValidator{}
		case int, int64:
			unwrapped = &goroku.IntegerValidator{}
		}
	}

	defValStr := prepValue(defVal)
	curValStr := prepValue(curVal)
	if isHidden && !forceHidden {
		curValStr = prepValue("••••••••")
	}

	typeHint := ""
	if unwrapped != nil {
		docName := m.getValidatorDocName(unwrapped)
		if docName != "" {
			engArt := ""
			firstChar := strings.ToLower(docName[:1])
			if strings.ContainsAny(firstChar, "euioay") {
				engArt = "n"
			}
			typehintTrans := m.getTrans("typehint", "🕵️ <b>Must be a{eng_art} {}</b>")
			typehintTrans = strings.ReplaceAll(typehintTrans, "{eng_art}", engArt)
			typehintTrans = strings.ReplaceAll(typehintTrans, "{}", docName)
			typehintTrans = strings.ReplaceAll(typehintTrans, "🕵️", m.getDetectiveEmoji())
			typeHint = typehintTrans
		}
	}

	configuringOptionTrans := m.getTrans("configuring_option", "<tg-emoji emoji-id=5341715473882955310>⚙️</tg-emoji> <b>Configuring option</b> <code>{0}</code> <b>of mod</b> <code>{1}</code>\n<i>ℹ️ {2}</i>\n\n<b>Default:</b> {3}\n\n<b>Current:</b> {4}\n\n{5}")
	configuringOptionTrans = strings.ReplaceAll(configuringOptionTrans, "ℹ️", m.getInfoEmoji())
	text := formatTrans(configuringOptionTrans, optionName, modName, doc, defValStr, curValStr, typeHint)
	if len(errMsgs) > 0 && errMsgs[0] != "" {
		text = fmt.Sprintf("%s <b>Validation Error: %s</b>\n\n", m.getValidationErrorEmoji(), errMsgs[0]) + text
	}

	var markup [][]inline.Button

	if isHidden {
		var btnText string
		var nextForce bool
		if forceHidden {
			btnText = m.getTrans("hide_value", "🔒 Hide value")
			nextForce = false
		} else {
			btnText = m.getTrans("show_hidden", "🚸 Show value")
			nextForce = true
		}
		markup = append(markup, []inline.Button{
			m.makeButton(btnText, func(call inline.CallbackQuery) error {
				return m.ConfigureOption(call, modName, optionName, nextForce, fromFolder)
			}),
		})
	}

	if isBool {
		curBool, _ := curVal.(bool)
		toggleText := fmt.Sprintf("❌ %s False", m.getTrans("set", "set"))
		if !curBool {
			toggleText = fmt.Sprintf("✅ %s True", m.getTrans("set", "set"))
		}
		markup = append(markup, []inline.Button{
			m.makeButton(toggleText, func(call inline.CallbackQuery) error {
				return m.SetBoolOption(call, modName, optionName, !curBool, fromFolder)
			}),
		})
	} else if isChoice {
		choiceVal := unwrapped.(*goroku.ChoiceValidator)
		var choiceRows [][]inline.Button
		var currentRow []inline.Button
		for _, v := range choiceVal.PossibleValues {
			vStr := fmt.Sprintf("%v", v)
			activeChar := "🔘"
			if fmt.Sprintf("%v", curVal) == vStr {
				activeChar = "☑️"
			}
			valOption := v
			currentRow = append(currentRow, m.makeButton(fmt.Sprintf("%s %s", activeChar, vStr), func(call inline.CallbackQuery) error {
				return m.SetChoiceOption(call, modName, optionName, valOption, fromFolder)
			}))
			if len(currentRow) == 2 {
				choiceRows = append(choiceRows, currentRow)
				currentRow = []inline.Button{}
			}
		}
		if len(currentRow) > 0 {
			choiceRows = append(choiceRows, currentRow)
		}
		markup = append(markup, choiceRows...)

		// Add "Enter value" button at the bottom of choices (Bug 5)
		markup = append(markup, []inline.Button{
			{
				Text:  m.getTrans("enter_value_btn", "✍️ Enter value"),
				Input: m.getTrans("enter_value_desc", "✍️ Enter new configuration value for this option"),
				InputHandler: func(call inline.CallbackQuery, inputVal string) error {
					return m.SetStringOption(call, modName, optionName, inputVal, fromFolder)
				},
			},
		})
	} else if isSeries {
		markup = append(markup, []inline.Button{
			{
				Text:  m.getTrans("add_item_btn", "➕ Add item"),
				Input: m.getTrans("add_item_desc", "✍️ Enter item to add"),
				InputHandler: func(call inline.CallbackQuery, inputVal string) error {
					return m.AddSeriesItem(call, modName, optionName, inputVal, fromFolder)
				},
			},
			{
				Text:  m.getTrans("remove_item_btn", "➖ Remove item"),
				Input: m.getTrans("remove_item_desc", "✍️ Enter item to remove"),
				InputHandler: func(call inline.CallbackQuery, inputVal string) error {
					return m.RemoveSeriesItem(call, modName, optionName, inputVal, fromFolder)
				},
			},
		})

		// Add "Enter value" button to set/replace the whole series (Bug 5)
		markup = append(markup, []inline.Button{
			{
				Text:  m.getTrans("enter_value_btn", "✍️ Enter value"),
				Input: m.getTrans("enter_value_desc", "✍️ Enter new configuration value for this option"),
				InputHandler: func(call inline.CallbackQuery, inputVal string) error {
					return m.SetStringOption(call, modName, optionName, inputVal, fromFolder)
				},
			},
		})
	} else {
		markup = append(markup, []inline.Button{
			{
				Text:  m.getTrans("enter_value_btn", "✍️ Enter value"),
				Input: m.getTrans("enter_value_desc", "✍️ Enter new configuration value for this option"),
				InputHandler: func(call inline.CallbackQuery, inputVal string) error {
					return m.SetStringOption(call, modName, optionName, inputVal, fromFolder)
				},
			},
		})
	}

	if fmt.Sprintf("%v", curVal) != fmt.Sprintf("%v", defVal) {
		markup = append(markup, []inline.Button{
			m.makeButton(m.getTrans("set_default_btn", "♻️ Reset default"), func(call inline.CallbackQuery) error {
				return m.ResetDefaultOption(call, modName, optionName, fromFolder)
			}),
		})
	}

	markup = append(markup, []inline.Button{
		m.makeBackButton(func(call inline.CallbackQuery) error {
			return m.ConfigureModule(call, modName, fromFolder)
		}),
		m.makeCloseButton(),
	})

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, markup, inline.WithStartText(m.getStartText()))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func (m *GorokuConfig) ShowOptionSavedScreen(call inline.CallbackQuery, modName, optionName string, fromFolder string) error {
	optionSavedTrans := m.getTrans("option_saved", "<tg-emoji emoji-id=5318933532825888187>⚙️</tg-emoji> <b>Option</b> <code>{0}</code> <b>of module</b> <code>{1}</code><b> saved!</b>\n<b>Current:</b> {2}")

	curVal := m.getOptionValue(modName, optionName)
	curValStr := prepValue(curVal)
	if m.isOptionHidden(modName, optionName) {
		curValStr = prepValue("••••••••")
	}

	text := formatTrans(optionSavedTrans, optionName, modName, curValStr)

	markup := [][]inline.Button{
		{
			m.makeBackButton(func(call inline.CallbackQuery) error {
				return m.ConfigureModule(call, modName, fromFolder)
			}),
			m.makeCloseButton(),
		},
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil {
		return call.Edit(text, im.GenerateMarkup(markup))
	}
	return nil
}

func (m *GorokuConfig) ShowOptionResetScreen(call inline.CallbackQuery, modName, optionName string, fromFolder string) error {
	optionResetTrans := m.getTrans("option_reset", "♻️ <b>Option</b> <code>{0}</code> <b>of module</b> <code>{1}</code> <b>has been reset to default</b>\n<b>Current:</b> {2}")

	curVal := m.getOptionValue(modName, optionName)
	curValStr := prepValue(curVal)

	text := formatTrans(optionResetTrans, optionName, modName, curValStr)

	markup := [][]inline.Button{
		{
			m.makeBackButton(func(call inline.CallbackQuery) error {
				return m.ConfigureModule(call, modName, fromFolder)
			}),
			m.makeCloseButton(),
		},
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil {
		return call.Edit(text, im.GenerateMarkup(markup))
	}
	return nil
}

func (m *GorokuConfig) SetBoolOption(call inline.CallbackQuery, modName, optionName string, val bool, fromFolder string) error {
	validatedVal, err := m.validateConfig(modName, optionName, val)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("❌ Error: %v", err), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, err.Error())
	}
	m.db.Set(modName, optionName, validatedVal)
	m.reloadModule(modName)
	_ = call.Answer("✅ Option saved!", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) SetChoiceOption(call inline.CallbackQuery, modName, optionName string, val interface{}, fromFolder string) error {
	validatedVal, err := m.validateConfig(modName, optionName, val)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("❌ Error: %v", err), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, err.Error())
	}
	m.db.Set(modName, optionName, validatedVal)
	m.reloadModule(modName)
	_ = call.Answer("✅ Option saved!", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) SetStringOption(call inline.CallbackQuery, modName, optionName string, val string, fromFolder string) error {
	var interfaceVal interface{}
	// Parse JSON or standard values (Bug 5)
	if err := json.Unmarshal([]byte(val), &interfaceVal); err != nil {
		lowerVal := strings.ToLower(val)
		if lowerVal == "true" {
			interfaceVal = true
		} else if lowerVal == "false" {
			interfaceVal = false
		} else if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			interfaceVal = i
		} else if f, err := strconv.ParseFloat(val, 64); err == nil {
			interfaceVal = f
		} else {
			interfaceVal = val
		}
	} else {
		// If it's a JSON array, convert []interface{} to []string for Series compatibility
		if arr, ok := interfaceVal.([]interface{}); ok {
			strList := make([]string, len(arr))
			for i, v := range arr {
				strList[i] = fmt.Sprintf("%v", v)
			}
			interfaceVal = strList
		}
	}

	validatedVal, err := m.validateConfig(modName, optionName, interfaceVal)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("❌ Error: %v", err), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, err.Error())
	}
	m.db.Set(modName, optionName, validatedVal)
	m.reloadModule(modName)
	_ = call.Answer("✅ Option saved!", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) ResetDefaultOption(call inline.CallbackQuery, modName, optionName string, fromFolder string) error {
	m.db.Delete(modName, optionName)
	m.reloadModule(modName)
	_ = call.Answer("♻️ Reset to default", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) AddSeriesItem(call inline.CallbackQuery, modName, optionName string, itemVal string, fromFolder string) error {
	curVal := m.getOptionValue(modName, optionName)
	var list []string
	if listStr, ok := curVal.(string); ok {
		list = strings.Split(listStr, ",")
	} else if listArr, ok := curVal.([]interface{}); ok {
		for _, item := range listArr {
			list = append(list, fmt.Sprintf("%v", item))
		}
	} else if listStrArr, ok := curVal.([]string); ok {
		list = listStrArr
	}

	// Split comma-separated inputs or parse JSON lists (Bug 6)
	var itemsToAdd []string
	var jsonVal interface{}
	if err := json.Unmarshal([]byte(itemVal), &jsonVal); err == nil {
		if arr, ok := jsonVal.([]interface{}); ok {
			for _, item := range arr {
				itemsToAdd = append(itemsToAdd, fmt.Sprintf("%v", item))
			}
		} else {
			itemsToAdd = append(itemsToAdd, fmt.Sprintf("%v", jsonVal))
		}
	} else {
		for _, part := range strings.Split(itemVal, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				itemsToAdd = append(itemsToAdd, trimmed)
			}
		}
	}

	list = append(list, itemsToAdd...)

	validatedVal, err := m.validateConfig(modName, optionName, list)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("❌ Error: %v", err), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, err.Error())
	}
	m.db.Set(modName, optionName, validatedVal)
	m.reloadModule(modName)
	_ = call.Answer("➕ Item added!", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) RemoveSeriesItem(call inline.CallbackQuery, modName, optionName string, itemVal string, fromFolder string) error {
	curVal := m.getOptionValue(modName, optionName)
	var list []string
	if listStr, ok := curVal.(string); ok {
		list = strings.Split(listStr, ",")
	} else if listArr, ok := curVal.([]interface{}); ok {
		for _, item := range listArr {
			list = append(list, fmt.Sprintf("%v", item))
		}
	} else if listStrArr, ok := curVal.([]string); ok {
		list = listStrArr
	}

	newList := []string{}
	found := false
	target := strings.TrimSpace(itemVal)
	for _, item := range list {
		trimmed := strings.TrimSpace(item)
		if trimmed == target {
			found = true
			continue
		}
		newList = append(newList, trimmed)
	}

	if !found {
		_ = call.Answer(fmt.Sprintf("❌ Error: Item %s not found in list", itemVal), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, fmt.Sprintf("Item %s not found in list", itemVal))
	}

	validatedVal, err := m.validateConfig(modName, optionName, newList)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("❌ Error: %v", err), true)
		return m.ConfigureOption(call, modName, optionName, false, fromFolder, err.Error())
	}
	m.db.Set(modName, optionName, validatedVal)
	m.reloadModule(modName)
	_ = call.Answer("➖ Item removed!", false)
	return m.ConfigureOption(call, modName, optionName, false, fromFolder)
}

func (m *GorokuConfig) textConfig(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		msg.Text = "❌ Error: Modules registry not found."
		msg.Answer(msg.Text)
		return nil
	}

	modulesList := loader.GetModules()
	dbData := m.db.GetAll()

	if rawArgs == "" {
		var text strings.Builder
		headerTrans := m.getTrans("header_modules", "⚙️ <b>Goroku Userbot Configuration</b>\n\nChoose a module to configure using <code>.config [module_name]</code> or set directly via <code>.fcfg [module] [key] [value]</code> / reset via <code>.dcfg [module] [key]</code>:\n\n")
		text.WriteString(headerTrans)

		modNames := make([]string, 0, len(modulesList))
		for name, mod := range modulesList {
			_, hasConfig := mod.(goroku.ModuleWithConfig)
			if hasConfig || strings.EqualFold(mod.Name(), "InlineStuff") {
				modNames = append(modNames, name)
			}
		}
		sort.Strings(modNames)

		for _, name := range modNames {
			mod := modulesList[name]
			if strings.EqualFold(mod.Name(), "InlineStuff") {
				customBot, _ := m.db.Get("goroku.inline", "custom_bot", "").(string)
				botToken, _ := m.db.Get("goroku.inline", "bot_token", "").(string)
				botTokenState := "not set"
				if botToken != "" {
					botTokenState = "set"
				}
				if customBot == "" {
					customBot = "not set"
				} else {
					customBot = "@" + strings.TrimPrefix(customBot, "@")
				}
				text.WriteString(fmt.Sprintf("• <b>%s</b>: <code>custom_bot=%s</code>, <code>bot_token=%s</code>\n", mod.Name(), customBot, botTokenState))
				text.WriteString("  <i>Use .ch_goroku_bot &lt;username&gt;, .ch_bot_token &lt;token&gt;, .inlineinfo</i>\n")
				continue
			}

			keys := []string{}
			if innerMap, exists := dbData[strings.ToLower(mod.Name())]; exists {
				for k := range innerMap {
					keys = append(keys, k)
				}
			}
			if innerMap, exists := dbData[mod.Name()]; exists {
				for k := range innerMap {
					found := false
					for _, existing := range keys {
						if existing == k {
							found = true
							break
						}
					}
					if !found {
						keys = append(keys, k)
					}
				}
			}

			if len(keys) > 0 {
				sort.Strings(keys)
				text.WriteString(fmt.Sprintf("• <b>%s</b>: <code>%s</code>\n", mod.Name(), strings.Join(keys, ", ")))
			} else {
				text.WriteString(fmt.Sprintf("• <b>%s</b>: <i>no custom settings</i>\n", mod.Name()))
			}
		}

		msg.Text = text.String()
		msg.Answer(msg.Text)
		return nil
	}

	targetMod := strings.ToLower(rawArgs)
	var found goroku.Module
	for _, mod := range modulesList {
		if strings.ToLower(mod.Name()) == targetMod {
			found = mod
			break
		}
	}

	if found == nil {
		msg.Text = m.getTrans("no_mod", "🚫 <b>Module doesn't exist</b>")
		msg.Answer(msg.Text)
		return nil
	}

	if _, hasConfig := found.(goroku.ModuleWithConfig); !hasConfig && !strings.EqualFold(found.Name(), "InlineStuff") {
		msg.Text = "🚫 <b>This module has no configuration options</b>"
		msg.Answer(msg.Text)
		return nil
	}

	var text strings.Builder
	modInfoTrans := m.getTrans("module_info", "⚙️ <b>Configuration of module</b> <code>%s</code>:\n\n")
	text.WriteString(fmt.Sprintf(modInfoTrans, found.Name()))
	if strings.EqualFold(found.Name(), "InlineStuff") {
		customBot, _ := m.db.Get("goroku.inline", "custom_bot", "").(string)
		if customBot == "" {
			customBot = "not set"
		} else {
			customBot = "@" + strings.TrimPrefix(customBot, "@")
		}
		botToken, _ := m.db.Get("goroku.inline", "bot_token", "").(string)
		botTokenState := "not set"
		if botToken != "" {
			parts := strings.SplitN(botToken, ":", 2)
			if len(parts) == 2 && len(parts[1]) > 6 {
				botTokenState = fmt.Sprintf("%s:%s...%s", parts[0], parts[1][:3], parts[1][len(parts[1])-3:])
			} else {
				botTokenState = "configured"
			}
		}
		text.WriteString(fmt.Sprintf("• <b>custom_bot</b> = <code>%s</code>\n", customBot))
		text.WriteString(fmt.Sprintf("• <b>bot_token</b> = <code>%s</code>\n", botTokenState))
		text.WriteString("\n<i>Inline bot is configured via .ch_goroku_bot &lt;username&gt;, .ch_bot_token &lt;token&gt; and checked via .inlineinfo, matching Python behavior.</i>\n")
		msg.Text = text.String()
		msg.Answer(msg.Text)
		return nil
	}

	keys := []string{}
	innerMapMerged := make(map[string]interface{})

	for _, owner := range []string{found.Name(), strings.ToLower(found.Name())} {
		if innerMap, exists := dbData[owner]; exists {
			for k, v := range innerMap {
				if _, ok := innerMapMerged[k]; !ok {
					keys = append(keys, k)
				}
				innerMapMerged[k] = v
			}
		}
	}

	if len(keys) == 0 {
		text.WriteString("<i>This module has no saved configurations in the database.</i>\n")
	} else {
		sort.Strings(keys)
		for _, k := range keys {
			val := innerMapMerged[k]
			valStr := fmt.Sprintf("%v", val)
			if bytes, err := json.Marshal(val); err == nil {
				valStr = string(bytes)
			}
			text.WriteString(fmt.Sprintf("• <b>%s</b> = <code>%s</code>\n", k, valStr))
		}
	}

	msg.Text = text.String()
	msg.Answer(msg.Text)
	return nil
}

func (m *GorokuConfig) DConfigCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	parts := []string{}
	for _, p := range strings.Split(rawArgs, "&&") {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	splitBySpace := func(s string) (string, string) {
		idx := -1
		for i, r := range s {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				idx = i
				break
			}
		}
		if idx == -1 {
			return s, ""
		}
		return s[:idx], strings.TrimSpace(s[idx:])
	}

	p0 := strings.TrimSpace(parts[0])
	mod, option := splitBySpace(p0)
	if option == "" {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		_ = msg.Answer("❌ Error: Modules registry not found.")
		return nil
	}

	targetModName := strings.ToLower(mod)
	var targetModule goroku.Module
	for _, modObj := range loader.GetModules() {
		if strings.ToLower(modObj.Name()) == targetModName {
			targetModule = modObj
			break
		}
	}

	if targetModule == nil {
		_ = msg.Answer(m.getTrans("no_mod", "🚫 <b>Module doesn't exist</b>"))
		return nil
	}

	if _, hasConfig := targetModule.(goroku.ModuleWithConfig); !hasConfig {
		_ = msg.Answer("🚫 <b>This module has no configuration options</b>")
		return nil
	}

	if !m.optionExists(targetModule, option) {
		_ = msg.Answer(m.getTrans("no_option", "🚫 <b>Configuration option doesn't exist</b>"))
		return nil
	}

	// Check all other options' existence
	for _, p := range parts[1:] {
		optName := strings.TrimSpace(p)
		if !m.optionExists(targetModule, optName) {
			_ = msg.Answer(m.getTrans("no_option", "🚫 <b>Configuration option doesn't exist</b>"))
			return nil
		}
	}

	applyReset := func(opt string) string {
		m.db.Delete(targetModule.Name(), opt)
		m.reloadModule(targetModule.Name())
		defVal := m.getDefaultValue(targetModule.Name(), opt)
		return prepValue(defVal)
	}

	resets := []string{}
	displayVal := applyReset(option)
	resetTrans := m.getTrans("option_reset", "♻️ <b>Option</b> <code>{0}</code> <b>of module</b> <code>{1}</code> <b>has been reset to default</b>\n<b>Current: {2}</b>")
	resets = append(resets, formatTrans(resetTrans, option, targetModule.Name(), displayVal))

	for _, p := range parts[1:] {
		optName := strings.TrimSpace(p)
		displayVal = applyReset(optName)
		resets = append(resets, formatTrans(resetTrans, optName, targetModule.Name(), displayVal))
	}

	_ = msg.Answer(strings.Join(resets, "\n"))
	return nil
}

func (m *GorokuConfig) FConfigCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	replyMsg, err := msg.GetReplyMessage()
	if err != nil {
		// ignore
	}

	parts := []string{}
	for _, p := range strings.Split(rawArgs, "&&") {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	splitBySpaceN := func(s string, n int) []string {
		var result []string
		start := 0
		count := 0
		for i, r := range s {
			if r == ' ' || r == '	' || r == '\n' || r == '\r' {
				if count < n {
					if i > start {
						result = append(result, strings.TrimSpace(s[start:i]))
					}
					start = i + 1
					count++
				}
			}
		}
		if start < len(s) {
			result = append(result, strings.TrimSpace(s[start:]))
		}
		return result
	}

	p0 := strings.TrimSpace(parts[0])
	firstParts := splitBySpaceN(p0, 2)
	if len(firstParts) < 2 {
		_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
		return nil
	}

	mod := firstParts[0]
	var option, value string
	if len(firstParts) == 3 {
		option = firstParts[1]
		value = firstParts[2]
	} else {
		option = firstParts[1]
		if replyMsg != nil {
			value = replyMsg.Text
		}
		if value == "" {
			_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
			return nil
		}
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		_ = msg.Answer("❌ Error: Modules registry not found.")
		return nil
	}

	targetModName := strings.ToLower(mod)
	var targetModule goroku.Module
	for _, modObj := range loader.GetModules() {
		if strings.ToLower(modObj.Name()) == targetModName {
			targetModule = modObj
			break
		}
	}

	if targetModule == nil {
		_ = msg.Answer(m.getTrans("no_mod", "🚫 <b>Module doesn't exist</b>"))
		return nil
	}

	if _, hasConfig := targetModule.(goroku.ModuleWithConfig); !hasConfig {
		_ = msg.Answer("🚫 <b>This module has no configuration options</b>")
		return nil
	}

	if !m.optionExists(targetModule, option) {
		_ = msg.Answer(m.getTrans("no_option", "🚫 <b>Configuration option doesn't exist</b>"))
		return nil
	}

	// Also check all other options' existence
	for _, p := range parts[1:] {
		seg := strings.SplitN(strings.TrimSpace(p), " ", 2)
		if len(seg) < 2 {
			_ = msg.Answer(m.getTrans("args", "🚫 <b>You specified incorrect args</b>"))
			return nil
		}
		optName := seg[0]
		if !m.optionExists(targetModule, optName) {
			_ = msg.Answer(m.getTrans("no_option", "🚫 <b>Configuration option doesn't exist</b>"))
			return nil
		}
	}

	applyUpdate := func(opt, valStr string) (string, error) {
		var val interface{} = valStr
		var jsonVal interface{}
		if err := json.Unmarshal([]byte(valStr), &jsonVal); err == nil {
			val = jsonVal
		} else {
			lowerVal := strings.ToLower(valStr)
			if lowerVal == "true" {
				val = true
			} else if lowerVal == "false" {
				val = false
			} else if i, err := strconv.ParseInt(valStr, 10, 64); err == nil {
				val = i
			} else if f, err := strconv.ParseFloat(valStr, 64); err == nil {
				val = f
			}
		}

		validatedVal, err := m.validateConfig(targetModule.Name(), opt, val)
		if err != nil {
			return "", err
		}

		m.db.Set(targetModule.Name(), opt, validatedVal)
		m.reloadModule(targetModule.Name())
		return fmt.Sprintf("%v", validatedVal), nil
	}

	updates := []string{}
	displayVal, err := applyUpdate(option, value)
	if err != nil {
		_ = msg.Answer(fmt.Sprintf("❌ <b>Validation failed:</b> %s", err.Error()))
		return nil
	}
	savedTrans := m.getTrans("option_saved", "⚙️ <b>Option</b> <code>{0}</code> <b>of module</b> <code>{1}</code><b> saved!</b>\n<b>Current: {2}</b>")
	if m.isOptionHidden(targetModule.Name(), option) {
		displayVal = "••••••••"
	}
	updates = append(updates, formatTrans(savedTrans, option, targetModule.Name(), displayVal))

	for _, p := range parts[1:] {
		seg := strings.SplitN(strings.TrimSpace(p), " ", 2)
		optName := seg[0]
		optVal := seg[1]

		displayVal, err = applyUpdate(optName, optVal)
		if err != nil {
			_ = msg.Answer(fmt.Sprintf("❌ <b>Validation failed for option %s:</b> %s", optName, err.Error()))
			return nil
		}
		if m.isOptionHidden(targetModule.Name(), optName) {
			displayVal = "••••••••"
		}
		updates = append(updates, formatTrans(savedTrans, optName, targetModule.Name(), displayVal))
	}

	_ = msg.Answer(strings.Join(updates, "\n"))
	return nil
}

var schemas = map[string]map[string]goroku.Validator{
	"gorokuconfig": {
		"cfg_emoji":   &goroku.StringValidator{},
		"start_emoji": &goroku.StringValidator{},
		"list_emoji":  &goroku.StringValidator{},
	},
	"gorokuinfo": {
		"custom_message": &goroku.StringValidator{},
		"banner_url":     &goroku.StringValidator{},
		"ping_emoji":     &goroku.StringValidator{},
		"quote_media":    &goroku.BooleanValidator{},
		"invert_media":   &goroku.BooleanValidator{},
		"show_goroku":    &goroku.BooleanValidator{},
	},
	"loader": {
		"modules_repo":     &goroku.StringValidator{},
		"additional_repos": &goroku.SeriesValidator{},
		"share_link":       &goroku.BooleanValidator{},
		"basic_auth":       &goroku.HiddenValidator{Inner: &goroku.RegExpValidator{Pattern: regexp.MustCompile(`^.*:.*$`)}},
		"command_emoji":    &goroku.StringValidator{},
	},
	"apilimiter": {
		"time_sample":       &goroku.IntegerValidator{},
		"threshold":         &goroku.IntegerValidator{},
		"local_floodwait":   &goroku.IntegerValidator{},
		"forbidden_methods": &goroku.SeriesValidator{},
	},
	"help": {
		"core_emoji":    &goroku.StringValidator{},
		"plain_emoji":   &goroku.StringValidator{},
		"empty_emoji":   &goroku.StringValidator{},
		"desc_icon":     &goroku.StringValidator{},
		"command_emoji": &goroku.StringValidator{},
		"banner_url":    &goroku.StringValidator{},
		"media_quote":   &goroku.BooleanValidator{},
		"invert_media":  &goroku.BooleanValidator{},
	},
	"translate": {
		"only_text": &goroku.BooleanValidator{},
		"provider":  &goroku.ChoiceValidator{PossibleValues: []interface{}{"telegram", "google"}},
	},
	"terminal": {
		"flood_wait_protect": &goroku.IntegerValidator{},
	},
	"settings": {
		"allow_nonstandart_prefixes": &goroku.BooleanValidator{},
		"alias_emoji":                &goroku.StringValidator{},
	},
	"tester": {
		"force_send_all":        &goroku.BooleanValidator{},
		"tglog_level":           &goroku.ChoiceValidator{PossibleValues: []interface{}{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL", "ALL"}},
		"ignore_common":         &goroku.BooleanValidator{},
		"disable_internet_warn": &goroku.BooleanValidator{},
		"custom_message":        &goroku.StringValidator{},
		"hint":                  &goroku.StringValidator{},
		"ping_emoji":            &goroku.StringValidator{},
		"banner_url":            &goroku.StringValidator{},
		"quote_media":           &goroku.BooleanValidator{},
		"invert_media":          &goroku.BooleanValidator{},
	},
	"updater": {
		"git_origin_url":        &goroku.StringValidator{},
		"disable_notifications": &goroku.BooleanValidator{},
		"autoupdate":            &goroku.BooleanValidator{},
	},
}

func (m *GorokuConfig) getValidator(modName, option string) goroku.Validator {
	modName = strings.ToLower(modName)
	option = strings.ToLower(option)

	if m.client != nil && m.client.Loader != nil {
		if loader, ok := m.client.Loader.(*goroku.Modules); ok && loader != nil {
			if targetMod := loader.LookupByName(modName); targetMod != nil {
				if withValidators, ok := targetMod.(goroku.ModuleWithConfigValidators); ok {
					for k, v := range withValidators.ConfigValidators() {
						if strings.ToLower(k) == option {
							return v
						}
					}
				}
			}
		}
	}

	if modSchemas, exists := schemas[modName]; exists {
		if val, exists := modSchemas[option]; exists {
			return val
		}
	}

	if modName == "goroku.inline" && option == "bot_token" {
		return &goroku.HiddenValidator{
			Inner: &goroku.RegExpValidator{
				Pattern: regexp.MustCompile(`^[0-9]{8,10}:[a-zA-Z0-9_-]{34,36}$`),
			},
		}
	}

	return nil
}

func (m *GorokuConfig) isOptionHidden(modName, option string) bool {
	val := m.getValidator(modName, option)
	if val != nil {
		if _, ok := val.(*goroku.HiddenValidator); ok {
			return true
		}
	}
	return false
}

func (m *GorokuConfig) validateConfig(modName, option string, value interface{}) (interface{}, error) {
	modName = strings.ToLower(modName)
	option = strings.ToLower(option)
	if val := m.getValidator(modName, option); val != nil {
		return val.Validate(value)
	}
	defVal := m.getDefaultValue(modName, option)
	switch defVal.(type) {
	case bool:
		return (&goroku.BooleanValidator{}).Validate(value)
	case int, int64:
		return (&goroku.IntegerValidator{}).Validate(value)
	}
	return value, nil
}
