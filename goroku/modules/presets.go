package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"html"
	"math/rand"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"go.uber.org/zap"
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
	goroku.Base
	// Narrow seams used by module transaction tests.
	installHotModuleApply func(*goroku.Message, string, string, []byte) error
	setLoadedModulesApply func(map[string]string) error
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

func (m *Presets) ClientReady() error {
	if m.DB.GetBool("Presets", "sent", false) {
		return nil
	}

	im := m.Client.GorokuInline
	if im != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					goroku.L().Error("panic in presets menu goroutine", zap.Any("panic", r))
				}
			}()
			for i := 0; i < 20; i++ {
				if im.IsComplete() {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if im.IsComplete() {
				if err := m.sendMenu(m.Client.TGID); err != nil {
					return
				}
				if err := m.DB.SetBool("Presets", "sent", true); err != nil {
					goroku.L().Error("background database write failed", zap.String("operation", "set"), zap.String("owner", "Presets"), zap.String("key", "sent"), zap.Error(err))
				}
			}
		}()
	}
	return nil
}
func (m *Presets) OnDlmod() error { return nil }

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
	// Native-code install/load paths are owner-only dangerous capabilities (M4.3).
	return map[string]goroku.CommandMeta{
		"preset":      {OnlyOwner: true},
		"presets":     {OnlyOwner: true},
		"addpreset":   {OnlyOwner: true},
		"delpreset":   {OnlyOwner: true},
		"listpresets": {OnlyOwner: true},
		"loadpreset": {
			Aliases:   []string{"lp"},
			OnlyOwner: true,
		},
		"addtofolder": {
			Aliases:   []string{"af"},
			OnlyOwner: true,
		},
		"folderload": {
			Aliases:   []string{"fl"},
			OnlyOwner: true,
		},
		"removefromfolder": {
			Aliases:   []string{"rff"},
			OnlyOwner: true,
		},
		"loadaliases": {
			Aliases:   []string{"la"},
			OnlyOwner: true,
		},
		"aliasload": {
			Aliases:   []string{"al"},
			OnlyOwner: true,
		},
	}
}

func (m *Presets) HandleBotPM(msg *tgbotapi.Message) {
	if msg == nil {
		return
	}

	if msg.Text == "/presets" && msg.From != nil && msg.From.ID == m.Client.TGID {
		_ = m.sendMenu(msg.Chat.ID)
	}
}

func (m *Presets) sendMenu(chatID int64) error {
	im := m.Client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	var btns [][]inline.Button
	for _, preset := range presetKeys() {
		p := preset
		title := m.T(fmt.Sprintf("_%s_title", p), p)
		btns = append(btns, []inline.Button{
			m.makeButton(title, func(call inline.CallbackQuery) error {
				return m.ChoosePresetDetail(call, p)
			}),
		})
	}

	btns = append(btns, []inline.Button{
		{
			Text: m.T("close_menu", "🙈 Close this menu"),
			Handler: func(call inline.CallbackQuery) error {
				return closeForm(call)
			},
		},
	})
	markup := im.GenerateMarkup(btns)

	welcomeText := m.T("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>")

	photoConfig := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/presets_cmd.png"))
	photoConfig.Caption = welcomeText
	photoConfig.ParseMode = tgbotapi.ModeHTML
	photoConfig.ReplyMarkup = markup

	_, err := im.GetBotAPI().Send(photoConfig)
	return err
}

func (m *Presets) makeButton(text string, handler func(inline.CallbackQuery) error) inline.Button {
	return inline.Button{
		Text:    text,
		Data:    fmt.Sprintf("prst_%d_%d", time.Now().UnixNano(), rand.Int63()), //nolint:gosec
		Handler: handler,
	}
}

func presetKeys() []string {
	keys := make([]string, 0, len(defaultPresets))
	for k := range defaultPresets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func moduleFileAndName(link string) (string, string) {
	fileName := path.Base(link)
	if parsed, err := url.Parse(link); err == nil && parsed.Path != "" {
		fileName = path.Base(parsed.Path)
	}
	return fileName, strings.TrimSuffix(fileName, ".go")
}

func extractStructName(source []byte, fallback string) string {
	if names, err := moduleStructNames(source); err == nil && len(names) > 0 {
		return names[0]
	}
	return fallback
}

func (m *Presets) _isInstalled(link string) bool {
	loadedMods := m.getLoadedModules()

	linkClean := strings.TrimSpace(strings.ToLower(link))
	for _, installed := range loadedMods {
		if strings.EqualFold(strings.TrimSpace(installed), linkClean) {
			return true
		}
	}
	return false
}

func (m *Presets) getLoadedModules() map[string]string {
	return m.DB.GetStringMap("Loader", "loaded_modules", nil)
}

func downloadPresetModuleURL(link string) ([]byte, error) {
	client := newModuleHTTPClient(10 * time.Second)
	return downloadModuleURL(client, link, maxModuleSourceBytes)
}

func (m *Presets) installDownloadedModule(msg *goroku.Message, modName, link string, body []byte) (goroku.Module, error) {
	destPath, err := runtimeModuleSourcePath(modName)
	if err == nil {
		err = ensureRuntimeModuleSourceDir()
	}
	if err != nil {
		return nil, err
	}

	loader := &LoaderModule{
		Base:                  goroku.NewBase(m.Client, m.DB),
		installHotModuleApply: m.installHotModuleApply,
		setLoadedModulesApply: m.setLoadedModulesApply,
	}
	return loader.installPersistedHotModule(msg, modName, destPath, link, body)
}

func (m *Presets) getCustomPresets() map[string][]string {
	return m.DB.GetStringMapStringSlice("Presets", "custom_presets", nil)
}

func (m *Presets) saveCustomPresets(presets map[string][]string) error {
	return m.DB.SetStringMapStringSlice("Presets", "custom_presets", presets)
}

func (m *Presets) ListPresetsCmd(msg *goroku.Message) error {
	var text strings.Builder
	text.WriteString(m.T("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>"))
	text.WriteString("\n\n<b>Available collections:</b>\n")

	for _, k := range presetKeys() {
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
	im := m.Client.GorokuInline
	if im != nil && im.IsComplete() {
		return m.ChoosePresetsMenu(msg)
	}
	return m.ListPresetsCmd(msg)
}

func (m *Presets) ChoosePresetsMenu(msg any) error {
	im := m.Client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	var btns [][]inline.Button
	for _, preset := range presetKeys() {
		p := preset
		title := m.T(fmt.Sprintf("_%s_title", p), p)
		btns = append(btns, []inline.Button{
			m.makeButton(title, func(call inline.CallbackQuery) error {
				return m.ChoosePresetDetail(call, p)
			}),
		})
	}

	btns = append(btns, []inline.Button{
		{
			Text: m.T("close_menu", "🙈 Close this menu"),
			Handler: func(call inline.CallbackQuery) error {
				return closeForm(call)
			},
		},
	})

	text := m.T("welcome", "👋 <b>Hi there! Tired of scrolling through endless modules in channels? Let me suggest you some pre-made collections.</b>")

	var err error
	if msgObj, ok := msg.(*goroku.Message); ok {
		_, err = im.Form(text, msgObj, btns, inline.WithForceMe(true))
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		err = callObj.Edit(text, im.GenerateMarkup(btns))
	}
	return err
}

func (m *Presets) ChoosePresetDetail(call inline.CallbackQuery, preset string) error {
	links := defaultPresets[preset]

	titleTrans := m.T("preset", "<b>{}:</b>\nℹ️ <i>{}</i>\n\n⚒ <b>Modules in this collection:</b>\n\n{}")

	titleTrans = strings.Replace(titleTrans, "{}", m.T(fmt.Sprintf("_%s_title", preset), preset), 1)
	titleTrans = strings.Replace(titleTrans, "{}", m.T(fmt.Sprintf("_%s_desc", preset), "Modules"), 1)

	var modBtns []inline.Button
	var textParts []string
	var toInstall []string

	for _, link := range links {
		_, modName := moduleFileAndName(link)

		isInstalled := m._isInstalled(link)
		status := "▫️"
		if isInstalled {
			status = m.T("already_installed", "✅ [Installed]")
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
	bottomRow = append(bottomRow, m.makeButton(m.T("back", "🔙 Back"), func(c inline.CallbackQuery) error {
		return m.ChoosePresetsMenu(c)
	}))

	if len(toInstall) > 0 {
		bottomRow = append(bottomRow, m.makeButton(m.T("install", "📦 Install"), func(c inline.CallbackQuery) error {
			return m.InstallPresetModules(c, preset, toInstall)
		}))
	}

	bottomRow = append(bottomRow, inline.Button{
		Text: m.T("close_btn", "🔻 Close"),
		Handler: func(c inline.CallbackQuery) error {
			return closeForm(c)
		},
	})
	markup = append(markup, bottomRow)

	return call.Edit(text, call.Manager.GenerateMarkup(markup))
}

func (m *Presets) InstallSingleModule(call inline.CallbackQuery, preset string, link string) error {
	if !requireOwnerCallback(m.Client, call, call.FromID) {
		return nil
	}
	_ = closeForm(call)

	progressMsgText := getTrans(m.Translator, "Loader", "loading_module_via_file", "<tg-emoji emoji-id=5873204392429096339>🔄</tg-emoji> Loading the module...")
	progressMsg, err := m.Client.SendMessage(goroku.ChatRefID(m.Client.TGID), progressMsgText)
	if err != nil {
		return err
	}

	progressMsgID := progressMsg.SentMessageID()
	msgObj := &goroku.Message{
		ID:     progressMsgID,
		ChatID: m.Client.TGID,
		Client: m.Client,
	}

	_, modName := moduleFileAndName(link)

	bodyBytes, err := downloadPresetModuleURL(link)
	if err != nil {
		_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, formatModuleInstallError(fmt.Errorf("download %s: %w", modName, err)))
		return nil
	}

	installed, err := m.installDownloadedModule(msgObj, modName, link, bodyBytes)
	if err != nil && !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, moduleTransactionReport("Preset module install", err))
		return nil
	}
	if installed != nil {
		card := formatModuleInstalledCard(installed, moduleCommandPrefix(m.DB, m.Client.TGID), sanitizedModuleSource(moduleSourceRepository, link), err, getTrans(m.Translator, "Loader", "loaded", defaultLoadedTemplate), defaultCommandEmoji, getTrans(m.Translator, "Loader", "undoc", "No docs"))
		_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, card)
	} else {
		_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, formatModuleInstallError(errors.New("installed module is missing from the runtime registry")))
	}
	return nil
}

func (m *Presets) InstallPresetModules(call inline.CallbackQuery, preset string, links []string) error {
	if !requireOwnerCallback(m.Client, call, call.FromID) {
		return nil
	}
	_ = closeForm(call)

	progressMsgText := fmt.Sprintf(m.T("installing", "⏳ <b>Installing preset</b> <code>%s</code><b>...</b>"), preset)
	progressMsg, err := m.Client.SendMessage(goroku.ChatRefID(m.Client.TGID), progressMsgText)
	if err != nil {
		return err
	}

	progressMsgID := progressMsg.SentMessageID()
	msgObj := &goroku.Message{
		ID:     progressMsgID,
		ChatID: m.Client.TGID,
		Client: m.Client,
	}

	installed := 0
	failed := 0
	var durabilityWarnings []error
	for i, link := range links {
		_, modName := moduleFileAndName(link)

		updateText := fmt.Sprintf(m.T("installing_module", "⏳ <b>Installing preset %s (%d/%d modules)... Installing module %s...</b>"), html.EscapeString(preset), i+1, len(links), html.EscapeString(modName))
		_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, updateText)
		bodyBytes, err := downloadPresetModuleURL(link)
		if err != nil {
			failed++
			continue
		}

		if _, err = m.installDownloadedModule(msgObj, modName, link, bodyBytes); err != nil {
			if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
				failed++
				continue
			}
			durabilityWarnings = append(durabilityWarnings, err)
		}
		installed++
		time.Sleep(500 * time.Millisecond)
	}

	summary := fmt.Sprintf("✅ <b>Preset installation complete</b>\n<blockquote><b>Preset:</b> %s\n<b>Installed:</b> %d\n<b>Failed:</b> %d</blockquote>", html.EscapeString(preset), installed, failed)
	if len(durabilityWarnings) > 0 {
		summary += fmt.Sprintf("\n⚠️ <i>%d module(s) are active with manifest durability warnings.</i>", len(durabilityWarnings))
	}
	if installed == 0 {
		summary = fmt.Sprintf("❌ <b>No preset modules were installed</b>\n<blockquote><b>Preset:</b> %s\n<b>Failed:</b> %d</blockquote>", html.EscapeString(preset), failed)
	}
	_, _ = m.Client.EditMessage(goroku.ChatRefID(m.Client.TGID), progressMsgID, summary)

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

		installed := 0
		failed := 0
		var durabilityWarnings []error
		for _, url := range modules {
			_, modName := moduleFileAndName(url)

			bodyBytes, err := downloadPresetModuleURL(url)
			if err != nil {
				failed++
				continue
			}

			if _, err := m.installDownloadedModule(msg, modName, url, bodyBytes); err != nil {
				if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
					failed++
					continue
				}
				durabilityWarnings = append(durabilityWarnings, err)
			}
			installed++
		}

		summary := fmt.Sprintf("✅ <b>Preset installation complete</b>\n<blockquote><b>Preset:</b> %s\n<b>Installed:</b> %d\n<b>Failed:</b> %d</blockquote>", html.EscapeString(presetName), installed, failed)
		if len(durabilityWarnings) > 0 {
			summary += fmt.Sprintf("\n⚠️ <i>%d module(s) are active with manifest durability warnings.</i>", len(durabilityWarnings))
		}
		if installed == 0 {
			summary = fmt.Sprintf("❌ <b>No preset modules were installed</b>\n<blockquote><b>Preset:</b> %s\n<b>Failed:</b> %d</blockquote>", html.EscapeString(presetName), failed)
		}
		return msg.Answer(summary)
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf(m.Strings()["preset_header"], presetName))

	for _, url := range modules {
		_, modName := moduleFileAndName(url)

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
		if err := m.saveCustomPresets(custom); err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to save preset: %v", err))
		}
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
		if err := m.saveCustomPresets(custom); err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to delete preset: %v", err))
		}
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
			if err := m.saveCustomPresets(custom); err != nil {
				return msg.Answer(fmt.Sprintf("❌ Failed to delete preset: %v", err))
			}
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
	err = m.Client.DownloadMedia(reply.Media, &buf)
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

	im := m.Client.GorokuInline
	if im != nil && im.IsComplete() {
		// Use an inline form for details and owner-authorized install actions.
		var modTextLines []string
		var toInstall []string
		var modBtns []inline.Button

		for _, link := range presetData.Modules {
			_, modName := moduleFileAndName(link)

			isInstalled := m._isInstalled(link)
			status := "▫️"
			if isInstalled {
				status = m.T("already_installed", "✅ [Installed]")
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
			bottomRow = append(bottomRow, m.makeButton(m.T("install", "📦 Install"), func(c inline.CallbackQuery) error {
				return m.InstallPresetModules(c, presetData.Name, toInstall)
			}))
		}
		bottomRow = append(bottomRow, inline.Button{
			Text: m.T("close_btn", "🔻 Close"),
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
	installed := 0
	failed := 0
	var durabilityWarnings []error

	for _, url := range presetData.Modules {
		_, modName := moduleFileAndName(url)

		if m._isInstalled(url) {
			continue
		}

		bodyBytes, err := downloadPresetModuleURL(url)
		if err != nil {
			failed++
			continue
		}

		if _, err := m.installDownloadedModule(msg, modName, url, bodyBytes); err != nil {
			if !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
				failed++
				continue
			}
			durabilityWarnings = append(durabilityWarnings, err)
		}
		installed++
	}

	summary := fmt.Sprintf("✅ <b>Preset installation complete</b>\n<blockquote><b>Preset:</b> %s\n<b>Installed:</b> %d\n<b>Failed:</b> %d</blockquote>", html.EscapeString(presetData.Name), installed, failed)
	if len(durabilityWarnings) > 0 {
		summary += fmt.Sprintf("\n⚠️ <i>%d module(s) are active with manifest durability warnings.</i>", len(durabilityWarnings))
	}
	if installed == 0 && failed == 0 {
		summary = "✅ <b>All modules in this preset are already installed.</b>"
	} else if installed == 0 {
		summary = fmt.Sprintf("❌ <b>No preset modules were installed</b>\n<blockquote><b>Preset:</b> %s\n<b>Failed:</b> %d</blockquote>", html.EscapeString(presetData.Name), failed)
	}
	return msg.Answer(summary)
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

	folders := m.DB.GetStringMapStringSlice("presets", "folders", nil)

	list := folders[folderName]
	found := false
	for _, mName := range list {
		if strings.EqualFold(mName, moduleName) {
			found = true
			break
		}
	}

	if found {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Module is already in folder %s</b>", folderName))
		return nil
	}

	loader := m.Client.Loader
	if loader == nil {
		_ = msg.Answer("❌ Modules registry not found.")
		return nil
	}

	target := loader.LookupByName(moduleName)
	if target == nil {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Module %s not found</b>", moduleName))
		return nil
	}

	folders[folderName] = append(list, target.Name())
	if err := m.DB.SetStringMapStringSlice("presets", "folders", folders); err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to save folder: %v", err))
	}

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

	folders := m.DB.GetStringMapStringSlice("presets", "folders", nil)

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

	if err := m.DB.SetStringMapStringSlice("presets", "folders", folders); err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to save folder: %v", err))
	}
	_ = msg.Answer(fmt.Sprintf("✅ <b>Module %s removed from folder %s</b>", parts[1], folderName))
	return nil
}

func (m *Presets) FolderLoadCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		_ = msg.Answer("🚫 <b>Usage: .folderload &lt;folder&gt;</b>")
		return nil
	}

	folders := m.DB.GetStringMapStringSlice("presets", "folders", nil)

	list, exists := folders[rawArgs]
	if !exists {
		_ = msg.Answer(fmt.Sprintf("🚫 <b>Folder %s not found</b>", rawArgs))
		return nil
	}

	loadedMods := m.getLoadedModules()

	var modules []string
	for _, moduleName := range list {
		// Look up in loaded mods mapping to get the github raw url
		for k, url := range loadedMods {
			if strings.EqualFold(k, moduleName) {
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
	_, err := m.Client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption)
	return err
}

func (m *Presets) LoadAliasesCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		_ = msg.Answer("❌ <b>Reply to an aliases .json file to load</b>")
		return nil
	}

	var buf bytes.Buffer
	err = m.Client.DownloadMedia(reply.Media, &buf)
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

	loader := m.Client.Loader
	if loader == nil {
		_ = msg.Answer("❌ Modules registry not found.")
		return nil
	}

	dbAliases := m.DB.GetAnyMap("Settings", "aliases", nil)

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

	if err := m.DB.SetAnyMap("Settings", "aliases", dbAliases); err != nil {
		for _, alias := range loaded {
			loader.RemoveAlias(alias)
		}
		return msg.Answer(fmt.Sprintf("❌ Failed to persist aliases: %v", err))
	}

	_ = msg.Answer(fmt.Sprintf("✅ <b>Imported aliases:</b>\n\n<blockquote expandable>%s</blockquote>", strings.Join(loaded, ", ")))
	return nil
}

func (m *Presets) AliasLoadCmd(msg *goroku.Message) error {
	loader := m.Client.Loader
	if loader == nil {
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
	_, err := m.Client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption)
	return err
}
