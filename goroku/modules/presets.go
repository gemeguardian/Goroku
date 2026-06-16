package modules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

var defaultPresets = map[string][]string{
	"fun": {
		"https://raw.githubusercontent.com/coddrago/modules/main/aniquotes.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/artai.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/inline_ghoul.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/lovemagic.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/mindgame.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/moonlove.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/scrolller.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/tictactoe.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/trashguy.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/truth_or_dare.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/sticks.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/premium_sticks.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/magictext.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/quotes.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/IrisLab.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/arts.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/Complements.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/Compliments.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/mazemod.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/randnum.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/DoxTool.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/randomizer.go",
	},
	"chat": {
		"https://raw.githubusercontent.com/coddrago/modules/main/activists.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/banstickers.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/inactive.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/keyword.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/tagall.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/BanMedia.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/swmute.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/filter.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/id.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/autoclicker.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/hikarichat.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/yg_checks.go",
	},
	"service": {
		"https://raw.githubusercontent.com/coddrago/modules/main/account_switcher.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/surl.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/httpsc.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/img2pdf.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/latex.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/pollplot.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/temp_chat.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/vtt.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/accounttime.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/searx.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/whois.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/Neofetch.go",
	},
	"downloaders": {
		"https://raw.githubusercontent.com/coddrago/modules/main/uploader.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/web2file.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/instsave.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/tikcock.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/downloader.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/dl_yt_previews.go",
		"https://raw.githubusercontent.com/coddrago/modules/main/kuploader.go",
	},
}

type Presets struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *Presets) Name() string {
	return "Presets"
}

func (m *Presets) Strings() map[string]string {
	return map[string]string{
		"name":              "Presets",
		"welcome":           "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>",
		"preset_header":     "<b>Preset %s:</b>\n\n⚒ <b>Modules in this collection:</b>\n\n",
		"installing":        "⏳ <b>Installing preset</b> <code>%s</code><b>...</b>",
		"installed":         "🎉 <b>Preset</b> <code>%s</code> <b>installed!</b>",
		"already_installed": "✅ [Installed]",
		"args":              "🚫 <b>Invalid arguments</b>",
		"preset_not_found":  "🚫 <b>Preset not found</b>",
		"preset_added":      "✅ <b>Module added to preset %s</b>",
		"preset_deleted":    "✅ <b>Preset/Module removed from %s</b>",
	}
}

func (m *Presets) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Presets) ClientReady() error {
	sent, ok := m.db.Get("Presets", "sent", false).(bool)
	if ok && sent {
		return nil
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil {
		go func() {
			for i := 0; i < 20; i++ {
				if im.IsComplete() {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if im.IsComplete() {
				m.db.Set("Presets", "sent", true)
				m.db.Save()
				_ = m.sendMenu(m.client.TGID)
			}
		}()
	}
	return nil
}
func (m *Presets) OnUnload() error { return nil }
func (m *Presets) OnDlmod() error  { return nil }

func (m *Presets) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"preset":           m.PresetCmd,
		"presets":          m.PresetsCmd,
		"addpreset":        m.AddPresetCmd,
		"delpreset":        m.DelPresetCmd,
		"listpresets":      m.ListPresetsCmd,
		"loadpreset":       m.LoadPresetCmd,
		"addtofolder":      m.AddToFolderCmd,
		"folderload":       m.FolderLoadCmd,
		"removefromfolder": m.RemoveFromFolderCmd,
		"loadaliases":      m.LoadAliasesCmd,
		"aliasload":        m.AliasLoadCmd,
	}
}

func (m *Presets) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"loadpreset": {
			Aliases: []string{"lp"},
		},
		"addtofolder": {
			Aliases: []string{"af"},
		},
		"folderload": {
			Aliases: []string{"fl"},
		},
		"removefromfolder": {
			Aliases: []string{"rff"},
		},
		"loadaliases": {
			Aliases: []string{"la"},
		},
		"aliasload": {
			Aliases: []string{"al"},
		},
	}
}

func (m *Presets) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *Presets) HandleBotPM(msg *tgbotapi.Message) {
	if msg == nil {
		return
	}

	if msg.Text == "/presets" && msg.From != nil && msg.From.ID == m.client.TGID {
		_ = m.sendMenu(msg.Chat.ID)
	}
}

func (m *Presets) sendMenu(chatID int64) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	var keys []string
	for k := range defaultPresets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var btns [][]inline.Button
	for _, preset := range keys {
		p := preset
		title := m.getTrans(fmt.Sprintf("_%s_title", p), p)
		btns = append(btns, []inline.Button{
			m.makeButton(title, func(call inline.CallbackQuery) error {
				return m.ChoosePresetDetail(call, p)
			}),
		})
	}

	btns = append(btns, []inline.Button{
		{
			Text: m.getTrans("close_menu", "🙈 Close this menu"),
			Handler: func(call inline.CallbackQuery) error {
				return closeForm(call)
			},
		},
	})
	markup := im.GenerateMarkup(btns)

	welcomeText := m.getTrans("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>")

	photoConfig := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/presets_cmd.png"))
	photoConfig.Caption = welcomeText
	photoConfig.ParseMode = tgbotapi.ModeHTML
	photoConfig.ReplyMarkup = markup

	_, err := im.GetBotAPI().Send(photoConfig)
	return err
}

func (m *Presets) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *Presets) makeButton(text string, handler func(inline.CallbackQuery) error) inline.Button {
	rand.Seed(time.Now().UnixNano())
	return inline.Button{
		Text:    text,
		Data:    fmt.Sprintf("prst_%d_%d", time.Now().UnixNano(), rand.Int63()),
		Handler: handler,
	}
}

func (m *Presets) _isInstalled(link string) bool {
	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	linkClean := strings.TrimSpace(strings.ToLower(link))
	for _, installed := range loadedMods {
		if strings.TrimSpace(strings.ToLower(installed)) == linkClean {
			return true
		}
	}
	return false
}

func (m *Presets) getCustomPresets() map[string][]string {
	res := make(map[string][]string)
	val := m.db.Get("Presets", "custom_presets", nil)
	if val == nil {
		return res
	}
	if bytes, err := json.Marshal(val); err == nil {
		json.Unmarshal(bytes, &res)
	}
	return res
}

func (m *Presets) saveCustomPresets(presets map[string][]string) {
	m.db.Set("Presets", "custom_presets", presets)
}

func (m *Presets) ListPresetsCmd(msg *goroku.Message) error {
	var text strings.Builder
	text.WriteString(m.getTrans("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>"))
	text.WriteString("\n\n<b>Available collections:</b>\n")

	var keys []string
	for k := range defaultPresets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		text.WriteString(fmt.Sprintf("• <b>%s</b> (%d modules)\n", k, len(defaultPresets[k])))
	}

	custom := m.getCustomPresets()
	if len(custom) > 0 {
		text.WriteString("\n<b>Custom collections:</b>\n")
		var customKeys []string
		for k := range custom {
			customKeys = append(customKeys, k)
		}
		sort.Strings(customKeys)
		for _, k := range customKeys {
			text.WriteString(fmt.Sprintf("• <b>%s</b> (%d modules)\n", k, len(custom[k])))
		}
	}

	text.WriteString("\nUse <code>.preset [name]</code> to see modules and install them.")
	msg.Text = text.String()
	return msg.Answer(msg.Text)
}

func (m *Presets) PresetsCmd(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		return m.ChoosePresetsMenu(msg)
	}
	return m.ListPresetsCmd(msg)
}

func (m *Presets) ChoosePresetsMenu(msg interface{}) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	var keys []string
	for k := range defaultPresets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var btns [][]inline.Button
	for _, preset := range keys {
		p := preset
		title := m.getTrans(fmt.Sprintf("_%s_title", p), p)
		btns = append(btns, []inline.Button{
			m.makeButton(title, func(call inline.CallbackQuery) error {
				return m.ChoosePresetDetail(call, p)
			}),
		})
	}

	btns = append(btns, []inline.Button{
		{
			Text: m.getTrans("close_menu", "🙈 Close this menu"),
			Handler: func(call inline.CallbackQuery) error {
				return closeForm(call)
			},
		},
	})

	text := m.getTrans("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>")

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, btns)
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(btns))
	}
	return err
}

func (m *Presets) ChoosePresetDetail(call inline.CallbackQuery, preset string) error {
	links := defaultPresets[preset]

	titleTrans := m.getTrans("preset", "<b>{}:</b>\nℹ️ <i>{}</i>\n\n⚒ <b>Modules in this collection:</b>\n\n{}")

	titleTrans = strings.Replace(titleTrans, "{}", m.getTrans(fmt.Sprintf("_%s_title", preset), preset), 1)
	titleTrans = strings.Replace(titleTrans, "{}", m.getTrans(fmt.Sprintf("_%s_desc", preset), "Modules"), 1)

	var modBtns []inline.Button
	var textParts []string
	var toInstall []string

	for _, link := range links {
		urlParts := strings.Split(link, "/")
		fileName := urlParts[len(urlParts)-1]
		modName := strings.TrimSuffix(fileName, ".go")

		isInstalled := m._isInstalled(link)
		status := "▫️"
		if isInstalled {
			status = m.getTrans("already_installed", "✅ [Installed]")
			textParts = append(textParts, fmt.Sprintf("%s <b>%s</b>", status, modName))
		} else {
			textParts = append(textParts, fmt.Sprintf("%s <b>%s</b>", status, modName))
			toInstall = append(toInstall, link)
			l := link
			modBtns = append(modBtns, m.makeButton(modName, func(c inline.CallbackQuery) error {
				return m.InstallSingleModule(c, preset, l)
			}))
		}
	}

	text := strings.Replace(titleTrans, "{}", strings.Join(textParts, "\n"), 1)

	var markup [][]inline.Button
	for i := 0; i < len(modBtns); i += 3 {
		end := i + 3
		if end > len(modBtns) {
			end = len(modBtns)
		}
		markup = append(markup, modBtns[i:end])
	}

	var bottomRow []inline.Button
	bottomRow = append(bottomRow, m.makeButton(m.getTrans("back", "🔙 Back"), func(c inline.CallbackQuery) error {
		return m.ChoosePresetsMenu(c)
	}))

	if len(toInstall) > 0 {
		bottomRow = append(bottomRow, m.makeButton(m.getTrans("install", "📦 Install"), func(c inline.CallbackQuery) error {
			return m.InstallPresetModules(c, preset, toInstall)
		}))
	}

	bottomRow = append(bottomRow, inline.Button{
		Text: m.getTrans("close_btn", "🔻 Close"),
		Handler: func(c inline.CallbackQuery) error {
			return closeForm(c)
		},
	})
	markup = append(markup, bottomRow)

	return call.Edit(text, call.Manager.GenerateMarkup(markup))
}

func (m *Presets) InstallSingleModule(call inline.CallbackQuery, preset string, link string) error {
	_ = closeForm(call)

	// Send message to notify installation start
	progressMsgText := fmt.Sprintf(m.getTrans("installing_module", "⏳ <b>Installing preset %s... Installing module %s...</b>"), preset, link)
	progressMsg, err := m.client.SendMessage(m.client.TGID, progressMsgText)
	if err != nil {
		return err
	}

	var progressMsgID int64
	if hasID, ok := progressMsg.(interface{ GetID() int64 }); ok {
		progressMsgID = hasID.GetID()
	} else if hasID, ok := progressMsg.(interface{ GetID() int }); ok {
		progressMsgID = int64(hasID.GetID())
	}
	msgObj := &goroku.Message{
		ID:     progressMsgID,
		ChatID: m.client.TGID,
		Client: m.client,
	}

	urlParts := strings.Split(link, "/")
	fileName := urlParts[len(urlParts)-1]
	modName := strings.TrimSuffix(fileName, ".go")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(link)
	if err != nil || resp.StatusCode != http.StatusOK {
		m.client.EditMessage(m.client.TGID, progressMsgID, fmt.Sprintf("❌ Failed to download module %s", modName))
		return nil
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		m.client.EditMessage(m.client.TGID, progressMsgID, fmt.Sprintf("❌ Failed to read module %s", modName))
		return nil
	}

	destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
	err = os.WriteFile(destPath, bodyBytes, 0644)
	if err != nil {
		m.client.EditMessage(m.client.TGID, progressMsgID, fmt.Sprintf("❌ Failed to save module %s to disk", modName))
		return nil
	}

	structName := modName
	goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
	if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
		structName = loc[1]
	}

	// Update loaded modules in DB
	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods)
		}
	}
	loadedMods[modName] = link
	m.db.Set("Loader", "loaded_modules", loadedMods)

	// Hot-load
	err = RegisterModulesAndRebuild(msgObj, []string{structName})
	if err != nil {
		m.client.EditMessage(m.client.TGID, progressMsgID, fmt.Sprintf("❌ Hot-load failed: %v", err))
	}
	return nil
}

func (m *Presets) InstallPresetModules(call inline.CallbackQuery, preset string, links []string) error {
	_ = closeForm(call)

	progressMsgText := fmt.Sprintf(m.getTrans("installing", "⏳ <b>Installing preset</b> <code>%s</code><b>...</b>"), preset)
	progressMsg, err := m.client.SendMessage(m.client.TGID, progressMsgText)
	if err != nil {
		return err
	}

	var progressMsgID int64
	if hasID, ok := progressMsg.(interface{ GetID() int64 }); ok {
		progressMsgID = hasID.GetID()
	} else if hasID, ok := progressMsg.(interface{ GetID() int }); ok {
		progressMsgID = int64(hasID.GetID())
	}
	msgObj := &goroku.Message{
		ID:     progressMsgID,
		ChatID: m.client.TGID,
		Client: m.client,
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods)
		}
	}

	var structNames []string
	for i, link := range links {
		urlParts := strings.Split(link, "/")
		fileName := urlParts[len(urlParts)-1]
		modName := strings.TrimSuffix(fileName, ".go")

		updateText := fmt.Sprintf(m.getTrans("installing_module", "⏳ <b>Installing preset %s (%d/%d modules)... Installing module %s...</b>"), preset, i+1, len(links), modName)
		m.client.EditMessage(m.client.TGID, progressMsgID, updateText)

		httpClient := &http.Client{Timeout: 10 * time.Second}
		resp, err := httpClient.Get(link)
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
		err = os.WriteFile(destPath, bodyBytes, 0644)
		if err != nil {
			continue
		}

		structName := modName
		goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
		if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
			structName = loc[1]
		}
		structNames = append(structNames, structName)
		loadedMods[modName] = link
		time.Sleep(500 * time.Millisecond)
	}

	m.db.Set("Loader", "loaded_modules", loadedMods)

	if len(structNames) > 0 {
		err = RegisterModulesAndRebuild(msgObj, structNames)
		if err != nil {
			m.client.EditMessage(m.client.TGID, progressMsgID, fmt.Sprintf("❌ Hot-load failed: %v", err))
		}
	} else {
		m.client.EditMessage(m.client.TGID, progressMsgID, "❌ No modules were loaded.")
	}

	return nil
}

func (m *Presets) PresetCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		return m.ListPresetsCmd(msg)
	}

	parts := strings.Fields(rawArgs)
	presetName := strings.ToLower(parts[0])
	var modules []string
	found := false

	for k, v := range defaultPresets {
		if strings.ToLower(k) == presetName {
			modules = v
			presetName = k
			found = true
			break
		}
	}

	if !found {
		custom := m.getCustomPresets()
		for k, v := range custom {
			if strings.ToLower(k) == presetName {
				modules = v
				presetName = k
				found = true
				break
			}
		}
	}

	if !found {
		msg.Text = m.Strings()["preset_not_found"]
		return msg.Answer(msg.Text)
	}

	if len(parts) >= 2 && strings.ToLower(parts[1]) == "install" {
		msg.Text = fmt.Sprintf(m.Strings()["installing"], presetName)
		_ = msg.Answer(msg.Text)

		loadedMods := make(map[string]string)
		val := m.db.Get("Loader", "loaded_modules", nil)
		if val != nil {
			if bytes, err := json.Marshal(val); err == nil {
				json.Unmarshal(bytes, &loadedMods)
			}
		}

		var structNames []string
		for _, url := range modules {
			urlParts := strings.Split(url, "/")
			fileName := urlParts[len(urlParts)-1]
			modName := strings.TrimSuffix(fileName, ".go")

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(url)
			if err != nil || resp.StatusCode != http.StatusOK {
				continue
			}
			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				continue
			}

			destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
			err = os.WriteFile(destPath, bodyBytes, 0644)
			if err != nil {
				continue
			}

			structName := modName
			goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
			if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
				structName = loc[1]
			}
			structNames = append(structNames, structName)

			loadedMods[modName] = url
		}

		m.db.Set("Loader", "loaded_modules", loadedMods)

		if len(structNames) > 0 {
			err := RegisterModulesAndRebuild(msg, structNames)
			if err != nil {
				_ = msg.Answer(fmt.Sprintf("❌ <b>Preset registration failed:</b> %v", err))
			}
		} else {
			_ = msg.Answer("❌ <b>No modules were installed.</b>")
		}
		return nil
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf(m.Strings()["preset_header"], presetName))

	for _, url := range modules {
		urlParts := strings.Split(url, "/")
		fileName := urlParts[len(urlParts)-1]
		modName := strings.TrimSuffix(fileName, ".go")

		isInstalled := m._isInstalled(url)
		status := "▫️"
		if isInstalled {
			status = m.Strings()["already_installed"]
		}
		text.WriteString(fmt.Sprintf("%s <b>%s</b> (<code>%s</code>)\n", status, modName, url))
	}

	text.WriteString(fmt.Sprintf("\nTo install this collection, run: <code>.preset %s install</code>", presetName))
	msg.Text = text.String()
	return msg.Answer(msg.Text)
}

func (m *Presets) AddPresetCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		msg.Text = m.Strings()["args"]
		return msg.Answer(msg.Text)
	}

	presetName := parts[0]
	moduleURL := parts[1]

	custom := m.getCustomPresets()
	list := custom[presetName]
	found := false
	for _, u := range list {
		if u == moduleURL {
			found = true
			break
		}
	}
	if !found {
		list = append(list, moduleURL)
		custom[presetName] = list
		m.saveCustomPresets(custom)
	}

	msg.Text = fmt.Sprintf(m.Strings()["preset_added"], presetName)
	return msg.Answer(msg.Text)
}

func (m *Presets) DelPresetCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	parts := strings.Fields(rawArgs)
	if len(parts) < 1 {
		msg.Text = m.Strings()["args"]
		return msg.Answer(msg.Text)
	}

	presetName := parts[0]
	custom := m.getCustomPresets()

	if len(parts) == 1 {
		delete(custom, presetName)
		m.saveCustomPresets(custom)
	} else {
		moduleURL := parts[1]
		list, exists := custom[presetName]
		if exists {
			newList := []string{}
			for _, u := range list {
				if u != moduleURL {
					newList = append(newList, u)
				}
			}
			if len(newList) == 0 {
				delete(custom, presetName)
			} else {
				custom[presetName] = newList
			}
			m.saveCustomPresets(custom)
		}
	}

	msg.Text = fmt.Sprintf(m.Strings()["preset_deleted"], presetName)
	return msg.Answer(msg.Text)
}

func (m *Presets) LoadPresetCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		_ = msg.Answer("❌ <b>Reply to a preset .json file to load</b>")
		return nil
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(reply.Media, &buf)
	if err != nil {
		_ = msg.Answer(fmt.Sprintf("❌ <b>Failed to download preset file:</b> %v", err))
		return nil
	}

	type PresetJSON struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Modules     []string `json:"modules"`
	}

	var presetData PresetJSON
	err = json.Unmarshal(buf.Bytes(), &presetData)
	if err != nil {
		_ = msg.Answer("❌ <b>Invalid JSON preset format</b>")
		return nil
	}

	if presetData.Name == "" || len(presetData.Modules) == 0 {
		_ = msg.Answer("❌ <b>Preset JSON must contain name and non-empty modules list</b>")
		return nil
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		// Use inline form for interactive confirmation & details
		var modTextLines []string
		var toInstall []string
		var modBtns []inline.Button

		for _, link := range presetData.Modules {
			urlParts := strings.Split(link, "/")
			fileName := urlParts[len(urlParts)-1]
			modName := strings.TrimSuffix(fileName, ".go")

			isInstalled := m._isInstalled(link)
			status := "▫️"
			if isInstalled {
				status = m.getTrans("already_installed", "✅ [Installed]")
				modTextLines = append(modTextLines, fmt.Sprintf("%s <b>%s</b>", status, modName))
			} else {
				modTextLines = append(modTextLines, fmt.Sprintf("%s <b>%s</b>", status, modName))
				toInstall = append(toInstall, link)
				l := link
				modBtns = append(modBtns, m.makeButton(modName, func(c inline.CallbackQuery) error {
					return m.InstallSingleModule(c, presetData.Name, l)
				}))
			}
		}

		text := fmt.Sprintf("<b>Preset file: %s</b>\nℹ️ <i>%s</i>\n\n⚒ <b>Modules:</b>\n\n%s", presetData.Name, presetData.Description, strings.Join(modTextLines, "\n"))

		var markup [][]inline.Button
		for i := 0; i < len(modBtns); i += 3 {
			end := i + 3
			if end > len(modBtns) {
				end = len(modBtns)
			}
			markup = append(markup, modBtns[i:end])
		}

		var bottomRow []inline.Button
		if len(toInstall) > 0 {
			bottomRow = append(bottomRow, m.makeButton(m.getTrans("install", "📦 Install"), func(c inline.CallbackQuery) error {
				return m.InstallPresetModules(c, presetData.Name, toInstall)
			}))
		}
		bottomRow = append(bottomRow, inline.Button{
			Text: m.getTrans("close_btn", "🔻 Close"),
			Handler: func(c inline.CallbackQuery) error {
				return closeForm(c)
			},
		})
		markup = append(markup, bottomRow)

		_, err = im.Form(text, msg, markup)
		return err
	}

	// Text fallback installation
	_ = msg.Answer(fmt.Sprintf("⏳ <b>Installing preset %s...</b>", presetData.Name))
	var structNames []string
	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytes, &loadedMods)
		}
	}

	for _, url := range presetData.Modules {
		urlParts := strings.Split(url, "/")
		fileName := urlParts[len(urlParts)-1]
		modName := strings.TrimSuffix(fileName, ".go")

		if m._isInstalled(url) {
			continue
		}

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
		err = os.WriteFile(destPath, bodyBytes, 0644)
		if err != nil {
			continue
		}

		structName := modName
		goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
		if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
			structName = loc[1]
		}
		structNames = append(structNames, structName)
		loadedMods[modName] = url
	}

	m.db.Set("Loader", "loaded_modules", loadedMods)

	if len(structNames) > 0 {
		return RegisterModulesAndRebuild(msg, structNames)
	}
	_ = msg.Answer("✅ <b>All modules in this preset are already installed!</b>")
	return nil
}

func (m *Presets) AddToFolderCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		_ = msg.Answer("🚫 <b>Usage: .addtofolder &lt;folder&gt; &lt;module&gt;</b>")
		return nil
	}

	folderName := parts[0]
	moduleName := parts[1]

	folders := make(map[string][]string)
	val := m.db.Get("presets", "folders", nil)
	if val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytes, &folders)
		}
	}

	list := folders[folderName]
	found := false
	for _, mName := range list {
		if strings.ToLower(mName) == strings.ToLower(moduleName) {
			found = true
			break
		}
	}

	if found {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Module is already in folder %s</b>", folderName))
		return nil
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		_ = msg.Answer("❌ Modules registry not found.")
		return nil
	}

	target := loader.LookupByName(moduleName)
	if target == nil {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Module %s not found</b>", moduleName))
		return nil
	}

	folders[folderName] = append(list, target.Name())
	m.db.Set("presets", "folders", folders)

	_ = msg.Answer(fmt.Sprintf("✅ <b>Module %s added to folder %s</b>", target.Name(), folderName))
	return nil
}

func (m *Presets) RemoveFromFolderCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	parts := strings.Fields(rawArgs)
	if len(parts) < 2 {
		_ = msg.Answer("🚫 <b>Usage: .removefromfolder &lt;folder&gt; &lt;module&gt;</b>")
		return nil
	}

	folderName := parts[0]
	moduleName := strings.ToLower(parts[1])

	folders := make(map[string][]string)
	val := m.db.Get("presets", "folders", nil)
	if val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytes, &folders)
		}
	}

	list, exists := folders[folderName]
	if !exists {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Folder %s not found</b>", folderName))
		return nil
	}

	newList := []string{}
	found := false
	for _, mName := range list {
		if strings.ToLower(mName) == moduleName {
			found = true
			continue
		}
		newList = append(newList, mName)
	}

	if !found {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Module %s is not in folder %s</b>", parts[1], folderName))
		return nil
	}

	if len(newList) == 0 {
		delete(folders, folderName)
	} else {
		folders[folderName] = newList
	}

	m.db.Set("presets", "folders", folders)
	_ = msg.Answer(fmt.Sprintf("✅ <b>Module %s removed from folder %s</b>", parts[1], folderName))
	return nil
}

func (m *Presets) FolderLoadCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		_ = msg.Answer("🚫 <b>Usage: .folderload &lt;folder&gt;</b>")
		return nil
	}

	folders := make(map[string][]string)
	val := m.db.Get("presets", "folders", nil)
	if val != nil {
		if bytes, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytes, &folders)
		}
	}

	list, exists := folders[rawArgs]
	if !exists {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Folder %s not found</b>", rawArgs))
		return nil
	}

	loadedMods := make(map[string]string)
	lVal := m.db.Get("Loader", "loaded_modules", nil)
	if lVal != nil {
		if bytes, err := json.Marshal(lVal); err == nil {
			json.Unmarshal(bytes, &loadedMods)
		}
	}

	var modules []string
	for _, moduleName := range list {
		// Look up in loaded mods mapping to get the github raw url
		for k, url := range loadedMods {
			if strings.ToLower(k) == strings.ToLower(moduleName) {
				modules = append(modules, url)
				break
			}
		}
	}

	if len(modules) == 0 {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>No external modules with URLs found in folder %s</b>", rawArgs))
		return nil
	}

	type ExportPreset struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Modules     []string `json:"modules"`
	}

	exportData := ExportPreset{
		Name:        rawArgs,
		Description: fmt.Sprintf("Exported folder: %s", rawArgs),
		Modules:     modules,
	}

	exportBytes, _ := json.MarshalIndent(exportData, "", "  ")

	filename := fmt.Sprintf("%s.json", rawArgs)
	caption := fmt.Sprintf("📁 <b>Folder %s exported as preset file</b>\n\n💡 Reply to this file with <code>.lp</code> to import it on another client", rawArgs)

	nr := &namedReader{r: bytes.NewReader(exportBytes), name: filename}
	_, err := m.client.SendFile(msg.ChatID, nr, caption)
	return err
}

func (m *Presets) LoadAliasesCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		_ = msg.Answer("❌ <b>Reply to an aliases .json file to load</b>")
		return nil
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(reply.Media, &buf)
	if err != nil {
		_ = msg.Answer(fmt.Sprintf("❌ <b>Failed to download aliases:</b> %v", err))
		return nil
	}

	type AliasItem struct {
		Alias   string `json:"alias"`
		Command string `json:"command"`
	}

	var data []AliasItem
	err = json.Unmarshal(buf.Bytes(), &data)
	if err != nil {
		_ = msg.Answer("❌ <b>Invalid JSON aliases format</b>")
		return nil
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		_ = msg.Answer("❌ Modules registry not found.")
		return nil
	}

	aliasesVal := m.db.Get("Settings", "aliases", map[string]interface{}{})
	dbAliases := make(map[string]interface{})
	if a, ok := aliasesVal.(map[string]interface{}); ok {
		dbAliases = a
	}

	loaded := []string{}
	for _, item := range data {
		alias := strings.ToLower(item.Alias)
		cmdStr := item.Command

		parts := strings.SplitN(cmdStr, " ", 2)
		cmd := strings.ToLower(parts[0])

		if loader.AddAlias(alias, cmd) {
			dbAliases[alias] = cmdStr
			loaded = append(loaded, alias)
		}
	}

	m.db.Set("Settings", "aliases", dbAliases)

	_ = msg.Answer(fmt.Sprintf("✅ <b>Imported aliases:</b>\n\n<blockquote expandable>%s</blockquote>", strings.Join(loaded, ", ")))
	return nil
}

func (m *Presets) AliasLoadCmd(msg *goroku.Message) error {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		_ = msg.Answer("❌ Modules registry not found.")
		return nil
	}

	aliases := loader.GetAliases()
	if len(aliases) == 0 {
		_ = msg.Answer("📋 <b>No aliases found</b>")
		return nil
	}

	type AliasItem struct {
		Alias   string `json:"alias"`
		Command string `json:"command"`
	}

	exportList := []AliasItem{}
	for alias, target := range aliases {
		exportList = append(exportList, AliasItem{Alias: alias, Command: target})
	}

	exportBytes, _ := json.MarshalIndent(exportList, "", "  ")

	filename := "aliases.json"
	caption := "📋 <b>Your aliases exported</b>\n\n💡 Reply to this file with <code>.la</code> to import it on another client"

	nr := &namedReader{r: bytes.NewReader(exportBytes), name: filename}
	_, err := m.client.SendFile(msg.ChatID, nr, caption)
	return err
}
