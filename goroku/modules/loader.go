package modules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
)

type LoaderModule struct {
	client          *goroku.CustomTelegramClient
	db              *goroku.Database
	translator      *goroku.Translator
	modulesRepo     string
	additionalRepos []string
	shareLink       bool
	basicAuth       string
	commandEmoji    string
}

func (m *LoaderModule) Name() string {
	return "Loader"
}

func (m *LoaderModule) Strings() map[string]string {
	return map[string]string{
		"name": "Loader",
		"_cfg_MODULES_REPO": "Main repository URL for downloading modules",
		"_cfg_ADDITIONAL_REPOS": "Additional repository URLs for downloading modules",
		"_cfg_share_link": "Share module link when sending .session files",
		"_cfg_basic_auth": "Basic auth credentials for remote updates (format user:password)",
		"_cfg_command_emoji": "Bullet emoji/tag for loading commands in help",
	}
}

func (m *LoaderModule) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *LoaderModule) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"MODULES_REPO":     "https://raw.githubusercontent.com/coddrago/modules/main",
		"ADDITIONAL_REPOS": []interface{}{},
		"share_link":       false,
		"basic_auth":       "",
		"command_emoji":    "<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>",
	}
}

func (m *LoaderModule) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["MODULES_REPO"].(string); ok {
		m.modulesRepo = val
	}
	if val, ok := config["share_link"].(bool); ok {
		m.shareLink = val
	}
	if val, ok := config["basic_auth"].(string); ok {
		m.basicAuth = val
	}
	if val, ok := config["command_emoji"].(string); ok {
		m.commandEmoji = val
	}
	if val, ok := config["ADDITIONAL_REPOS"].([]interface{}); ok {
		m.additionalRepos = []string{}
		for _, item := range val {
			if s, ok := item.(string); ok {
				m.additionalRepos = append(m.additionalRepos, s)
			}
		}
	}
	return nil
}

func (m *LoaderModule) ClientReady() error {
	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val == nil {
		return nil
	}
	if bytesData, err := json.Marshal(val); err == nil {
		json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return nil
	}

	var structNames []string
	goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
	for modName, source := range loadedMods {
		path := filepath.Join(goroku.BasePath, "goroku", "modules", modName+".go")
		bodyBytes, err := os.ReadFile(path)
		if err != nil {
			if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
				bodyBytes, err = m.restoreLoadedModule(modName, source, path)
				if err != nil {
					continue
				}
			} else {
				continue
			}
		}
		structName := modName
		if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
			structName = loc[1]
		}
		structNames = append(structNames, structName)
	}

	if len(structNames) == 0 {
		return nil
	}
	return HotLoadStructs(loader, structNames)
}

func (m *LoaderModule) restoreLoadedModule(modName, url, path string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(url), ".py") || strings.HasSuffix(strings.ToLower(modName), ".py") {
		return nil, fmt.Errorf("python module %s cannot be restored by Go loader", modName)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("module restore failed with HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		return nil, err
	}
	return body, nil
}

func (m *LoaderModule) OnUnload() error { return nil }
func (m *LoaderModule) OnDlmod() error  { return nil }

func (m *LoaderModule) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"loadmod":      m.LoadmodCmd,
		"unloadmod":    m.UnloadmodCmd,
		"dlmod":        m.DlmodCmd,
		"clearmodules": m.ClearmodulesCmd,
		"addrepo":      m.AddrepoCmd,
		"delrepo":      m.DelrepoCmd,
		"modload":      m.ModloadCmd,
	}
}

func (m *LoaderModule) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"loadmod": {
			Aliases: []string{"lm"},
		},
		"unloadmod": {
			Aliases: []string{"ulm"},
		},
		"dlmod": {
			Aliases: []string{"dlm"},
		},
		"modload": {
			Aliases: []string{"ml"},
		},
	}
}

func (m *LoaderModule) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *LoaderModule) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *LoaderModule) getRepo(repo string) ([]string, error) {
	repo = strings.TrimSuffix(repo, "/")
	url := fmt.Sprintf("%s/full.txt", repo)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if m.basicAuth != "" {
		parts := strings.SplitN(m.basicAuth, ":", 2)
		if len(parts) == 2 {
			req.SetBasicAuth(parts[0], parts[1])
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var modules []string
	lines := strings.Split(string(bodyBytes), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			modules = append(modules, line)
		}
	}

	return modules, nil
}

func (m *LoaderModule) getRepoList() (map[string][]string, error) {
	repos := []string{m.modulesRepo}
	repos = append(repos, m.additionalRepos...)

	res := make(map[string][]string)
	for _, repo := range repos {
		if !strings.HasPrefix(repo, "http") {
			continue
		}
		mods, err := m.getRepo(repo)
		if err == nil {
			res[repo] = mods
		}
	}
	return res, nil
}

func (m *LoaderModule) findLink(moduleName string) (string, error) {
	repoList, err := m.getRepoList()
	if err != nil {
		return "", err
	}

	moduleNameLower := strings.ToLower(moduleName)
	for repo, mods := range repoList {
		for _, modPath := range mods {
			parts := strings.Split(modPath, "/")
			fileName := parts[len(parts)-1]
			cleanName := strings.TrimSuffix(fileName, ".go")
			if strings.ToLower(cleanName) == moduleNameLower || strings.ToLower(fileName) == moduleNameLower+".go" {
				// В Python get_repo_list возвращает полный URL или относительный.
				// Наш getRepo возвращает просто имена модулей из full.txt.
				// Поэтому полный URL строится как: repo + "/" + modPath (или если в full.txt уже лежит имя модуля, то repo + "/" + modPath + ".go")
				fullURL := fmt.Sprintf("%s/%s", strings.TrimSuffix(repo, "/"), strings.TrimPrefix(modPath, "/"))
				if !strings.HasSuffix(fullURL, ".go") {
					fullURL += ".go"
				}
				return fullURL, nil
			}
		}
	}
	return "", fmt.Errorf("module not found")
}

func (m *LoaderModule) DlmodCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		im, ok := m.client.GorokuInline.(*inline.InlineManager)
		if ok && im != nil {
			repoList, err := m.getRepoList()
			if err == nil && len(repoList) > 0 {
				var pages []string
				for repo, mods := range repoList {
					sort.Strings(mods)
					var escaped []string
					for _, mod := range mods {
						name := strings.TrimSuffix(mod, ".go")
						escaped = append(escaped, fmt.Sprintf("<code>%s</code>", utils.EscapeHTML(name)))
					}

					var chunkedRows []string
					for i := 0; i < len(escaped); i += 5 {
						end := i + 5
						if end > len(escaped) {
							end = len(escaped)
						}
						chunkedRows = append(chunkedRows, strings.Join(escaped[i:end], " | "))
					}

					pageText := m.getTrans("avail_header", "🎢 <b>Modules from repo</b>") + "\n☁️ " + repo + "\n\n" + strings.Join(chunkedRows, "\n")
					pages = append(pages, pageText)
				}

				_, err = im.List(msg, pages)
				if err == nil {
					return nil
				}
			}
		}

		msg.Text = m.getTrans("args", "🚫 <b>You must specify arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	url := rawArgs
	var modName string

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		msg.Text = m.getTrans("finding_module_in_repos", "🔄 Looking for modules in repositories.")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}

		foundURL, err := m.findLink(url)
		if err != nil {
			msg.Text = m.getTrans("no_module", "🚫 <b>Module not available in repo.</b>")
			if msg.Client != nil {
				_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
			}
			return nil
		}
		url = foundURL
		modName = rawArgs
	} else {
		parts := strings.Split(url, "/")
		fileName := parts[len(parts)-1]
		if strings.HasSuffix(fileName, ".py") {
			msg.Text = "❌ <b>Python modules (.py) cannot be loaded in the Go userbot port. Please provide a Go (.go) module instead.</b>"
			if msg.Client != nil {
				_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
			}
			return nil
		}
		modName = strings.TrimSuffix(fileName, ".go")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		msg.Text = m.getTrans("no_module", "🚫 <b>Module not available in repo.</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		msg.Text = m.getTrans("no_module", "🚫 <b>Module not available in repo.</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	fileName := modName + ".go"
	destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
	err = os.WriteFile(destPath, bodyBytes, 0644)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to save file to filesystem: %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}
	loadedMods[modName] = url
	m.db.Set("Loader", "loaded_modules", loadedMods)
	m.db.Save()

	structName := modName
	goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
	if loc := goReg.FindStringSubmatch(string(bodyBytes)); len(loc) == 2 {
		structName = loc[1]
	}

	err = m.registerHotLoad(msg, structName, destPath)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ <b>Load failed:</b> %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
	}

	return nil
}

func (m *LoaderModule) LoadmodCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		msg.Text = m.getTrans("provide_module", "⚠️ <b>Provide a module to load</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	modName := "custom_module"
	isGo := true

	goReg := regexp.MustCompile(`type\s+(\w+)\s+struct`)
	pyReg := regexp.MustCompile(`class\s+(\w+)\(loader\.Module\):`)

	if loc := goReg.FindStringSubmatch(rawArgs); len(loc) == 2 {
		modName = loc[1]
		isGo = true
	} else if loc := pyReg.FindStringSubmatch(rawArgs); len(loc) == 2 {
		modName = loc[1]
		isGo = false
	}

	if !isGo {
		msg.Text = "❌ <b>Python modules (.py) cannot be loaded in the Go userbot port. Please provide a Go (.go) module instead.</b>"
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	fileName := modName + ".go"
	destPath := filepath.Join(goroku.BasePath, "goroku", "modules", fileName)
	err := os.WriteFile(destPath, []byte(rawArgs), 0644)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ Failed to save module file: %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}
	loadedMods[modName] = "local"
	m.db.Set("Loader", "loaded_modules", loadedMods)
	m.db.Save()

	err = m.registerHotLoad(msg, modName, destPath)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ <b>Load failed:</b> %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
	}

	return nil
}

func (m *LoaderModule) UnloadmodCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		msg.Text = m.getTrans("no_class", "<b>What class needs to be unloaded?</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	modName := strings.ToLower(rawArgs)
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		msg.Text = "❌ Modules registry not found."
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	// Check if this is a system module (statically registered, not in loaded_modules)
	isSystem := true
	for k := range loadedMods {
		if strings.ToLower(k) == modName {
			isSystem = false
			break
		}
	}

	var foundName string
	for name := range loader.GetModules() {
		if strings.ToLower(name) == modName {
			foundName = name
			break
		}
	}

	if foundName == "" {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	if isSystem {
		msg.Text = formatTrans(m.getTrans("system_unload_forbidden", "🚫 <b>Module {} is a system module and cannot be unloaded.</b>"), foundName)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	err := loader.UnloadModule(foundName)
	if err != nil {
		msg.Text = formatTrans(m.getTrans("not_unloaded", "🚫 <b>Module not unloaded: {}</b>"), err.Error())
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	destPathGo := filepath.Join(goroku.BasePath, "goroku", "modules", foundName+".go")
	_ = os.Remove(destPathGo)

	for k := range loadedMods {
		if strings.ToLower(k) == modName {
			delete(loadedMods, k)
		}
	}
	m.db.Set("Loader", "loaded_modules", loadedMods)
	m.db.Save()

	err = m.unregisterHotLoad(msg, foundName)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ <b>Unload failed:</b> %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
	}

	return nil
}

func (m *LoaderModule) ClearmodulesCmd(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return m.executeClearModules(msg)
	}

	confirmText := m.getTrans("confirm_clearmodules", "⚠️ <b>Are you sure you want to clear all modules?</b>")
	markup := [][]inline.Button{
		{
			{
				Text: m.getTrans("clearmodules", "🗑 Clear modules"),
				Data: "clear_mods_confirm",
				Handler: func(c inline.CallbackQuery) error {
					_ = c.Answer("Deleting modules...", false)
					_, _ = c.BotMessage.Delete()

					loadedMods := make(map[string]string)
					val := m.db.Get("Loader", "loaded_modules", nil)
					if val != nil {
						if bytesData, err := json.Marshal(val); err == nil {
							json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
						}
					}

					for modName := range loadedMods {
						path := filepath.Join(goroku.BasePath, "goroku", "modules", modName+".go")
						_ = os.Remove(path)
					}

					m.db.Set("Loader", "loaded_modules", make(map[string]string))
					m.db.Save()

					replyMsg := tgbotapi.NewMessage(c.ChatID, m.getTrans("all_modules_deleted", "✅ All modules deleted"))
					_, _ = im.GetBotAPI().Send(replyMsg)

					go func() {
						time.Sleep(1 * time.Second)
						goroku.Restart()
					}()
					return nil
				},
			},
			{
				Text: m.getTrans("cancel", "Cancel"),
				Data: "clear_mods_cancel",
				Handler: func(c inline.CallbackQuery) error {
					_, err := c.BotMessage.Delete()
					return err
				},
			},
		},
	}

	_, err := im.Form(confirmText, msg.ChatID, markup)
	return err
}

func (m *LoaderModule) executeClearModules(msg *goroku.Message) error {
	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	for modName := range loadedMods {
		path := filepath.Join(goroku.BasePath, "goroku", "modules", modName+".go")
		_ = os.Remove(path)
	}

	m.db.Set("Loader", "loaded_modules", make(map[string]string))
	m.db.Save()

	msg.Text = m.getTrans("all_modules_deleted", "✅ All modules deleted")
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
	return nil
}

func (m *LoaderModule) AddrepoCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" || (!strings.HasPrefix(rawArgs, "http://") && !strings.HasPrefix(rawArgs, "https://")) {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	rawArgs = strings.TrimSuffix(rawArgs, "/")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/full.txt", rawArgs))
	if err != nil || resp.StatusCode != http.StatusOK {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}
	resp.Body.Close()

	exists := false
	for _, r := range m.additionalRepos {
		if r == rawArgs {
			exists = true
			break
		}
	}

	if exists {
		msg.Text = formatTrans(m.getTrans("repo_exists", "🚫 <b>Repository {} already exists</b>"), rawArgs)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	m.additionalRepos = append(m.additionalRepos, rawArgs)
	m.db.Set("Loader", "ADDITIONAL_REPOS", m.additionalRepos)
	m.db.Save()

	msg.Text = formatTrans(m.getTrans("repo_added", "✅ <b>Repository {} added</b>"), rawArgs)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *LoaderModule) DelrepoCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" || (!strings.HasPrefix(rawArgs, "http://") && !strings.HasPrefix(rawArgs, "https://")) {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	rawArgs = strings.TrimSuffix(rawArgs, "/")

	idx := -1
	for i, r := range m.additionalRepos {
		if r == rawArgs {
			idx = i
			break
		}
	}

	if idx == -1 {
		msg.Text = m.getTrans("repo_not_exists", "🚫 <b>Repository not found in your list</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	m.additionalRepos = append(m.additionalRepos[:idx], m.additionalRepos[idx+1:]...)
	m.db.Set("Loader", "ADDITIONAL_REPOS", m.additionalRepos)
	m.db.Save()

	msg.Text = formatTrans(m.getTrans("repo_deleted", "✅ <b>Repository {} deleted</b>"), rawArgs)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *LoaderModule) ModloadCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		msg.Text = m.getTrans("args", "🚫 <b>You must specify arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		msg.Text = "❌ Modules registry not found."
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	modulesList := loader.GetModules()
	var foundMod goroku.Module
	var class_name string

	for name, mod := range modulesList {
		if strings.ToLower(name) == strings.ToLower(rawArgs) || strings.ToLower(mod.Name()) == strings.ToLower(rawArgs) {
			foundMod = mod
			class_name = name
			break
		}
	}

	if foundMod == nil {
		for name, mod := range modulesList {
			if strings.Contains(strings.ToLower(name), strings.ToLower(rawArgs)) || strings.Contains(strings.ToLower(mod.Name()), strings.ToLower(rawArgs)) {
				foundMod = mod
				class_name = name
				break
			}
		}
	}

	if foundMod == nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	path, err := findModuleSource(class_name)
	if err != nil {
		path, err = findModuleSource(foundMod.Name())
	}

	if err != nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	prefix := "."
	if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefix = pVal
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	url := loadedMods[class_name]
	if url == "" {
		url = loadedMods[foundMod.Name()]
	}

	var text string
	if url != "" && strings.HasPrefix(url, "http") {
		template := m.getTrans("link", "<tg-emoji emoji-id=5256113064821926998>📁</tg-emoji> <b>File of</b> {class_name}\n\n<tg-emoji emoji-id=5134452506935427991>🪐</tg-emoji> <b>{prefix}lm in reply to this message to install</b>\n\n<tg-emoji emoji-id=4916086774649848789>🔗</tg-emoji> <code>{prefix}dlm {url}</code>\n\n{not_exact}")
		text = template
		text = strings.ReplaceAll(text, "{class_name}", class_name)
		text = strings.ReplaceAll(text, "{prefix}", prefix)
		text = strings.ReplaceAll(text, "{url}", url)
		text = strings.ReplaceAll(text, "{not_exact}", "")
	} else {
		template := m.getTrans("file", "<tg-emoji emoji-id=5256113064821926998>📁</tg-emoji> <b>File of</b> {class_name}\n\n<tg-emoji emoji-id=5134452506935427991>🪐</tg-emoji> <b>{prefix}lm in reply to this message to install</b>\n\n{not_exact}")
		text = template
		text = strings.ReplaceAll(text, "{class_name}", class_name)
		text = strings.ReplaceAll(text, "{prefix}", prefix)
		text = strings.ReplaceAll(text, "{not_exact}", "")
	}

	if msg.Client != nil {
		nr := &namedReader{r: bytes.NewReader(fileBytes), name: class_name + ".go"}
		var opts []goroku.MsgOption
		if msg.ReplyToMsgID != 0 {
			opts = append(opts, goroku.WithReplyTo(int64(msg.ReplyToMsgID)))
		}

		_ = msg.Delete()
		_, err = m.client.SendFileWithOptions(msg.ChatID, nr, text, opts...)
		return err
	}

	return nil
}

func (m *LoaderModule) registerHotLoad(msg *goroku.Message, structName string, codePath string) error {
	err := RegisterModulesAndRebuild(msg, []string{structName})
	if err != nil {
		_ = os.Remove(codePath)
		return err
	}
	return nil
}

func (m *LoaderModule) unregisterHotLoad(msg *goroku.Message, structName string) error {
	trans := m.getTrans("unloaded", "{} <b>Module {} unloaded.</b>")
	trans = strings.Replace(trans, "{}", "<tg-emoji emoji-id=5784993237412351403>✅</tg-emoji>", 1)
	trans = strings.Replace(trans, "{}", structName, 1)
	_ = msg.Answer(trans)
	return nil
}
