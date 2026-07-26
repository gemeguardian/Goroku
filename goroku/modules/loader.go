package modules

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"go.uber.org/zap"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
)

const (
	maxModuleSourceBytes = 2 * 1024 * 1024
	maxRepoIndexBytes    = 1024 * 1024
)

func validateRemoteURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("remote modules require HTTPS")
	}
	if u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("URL must contain a host and no embedded credentials")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("local addresses are not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicRemoteIP(ip) {
		return fmt.Errorf("non-public address is not allowed")
	}
	return nil
}

func isPublicRemoteIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// Block CGNAT (RFC 6598) 100.64.0.0/10 used by carrier-grade NAT / some cloud metadata paths.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false
	}
	return true
}

func newModuleHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("host resolved to no addresses")
		}
		for _, resolved := range ips {
			if !isPublicRemoteIP(resolved.IP) {
				return nil, fmt.Errorf("host resolves to non-public address %s", resolved.IP)
			}
		}
		dialer := &net.Dialer{Timeout: timeout}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validateRemoteURL(req.URL.String())
		},
	}
}

func downloadModuleURL(client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	if err := validateRemoteURL(rawURL); err != nil {
		return nil, err
	}
	return utils.DownloadURLLimited(client, rawURL, maxBytes)
}

type LoaderModule struct {
	client          *goroku.CustomTelegramClient
	db              *goroku.Database
	translator      *goroku.Translator
	modulesRepo     string
	additionalRepos []string
	basicAuth       string
	commandEmoji    string
	readyMu         sync.RWMutex
	fullyLoaded     bool
	restoreComplete bool
	restoreDone     chan struct{}

	// Narrow seam used by module transaction tests.
	installHotModuleApply func(*goroku.Message, string, string, []byte) error
	setLoadedModulesApply func(map[string]string) error
}

func (m *LoaderModule) Name() string {
	return "Loader"
}

func (m *LoaderModule) Strings() map[string]string {
	return map[string]string{
		"name":                  "Loader",
		"_cmd_doc_loadmod":      "Install native Go source from a replied file or command body. Native modules execute arbitrary code in this process; use only trusted source.",
		"_cmd_doc_dlmod":        "Download and install a native Go module. Native modules execute arbitrary code in this process; use only trusted repositories and URLs.",
		"_cfg_MODULES_REPO":     "Main repository URL for downloading modules",
		"_cfg_ADDITIONAL_REPOS": "Additional repository URLs for downloading modules",
		"_cfg_basic_auth":       "Basic auth credentials for remote updates (format user:password)",
		"_cfg_command_emoji":    "Bullet emoji/tag for loading commands in help",
	}
}

func (m *LoaderModule) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	loadedMods := db.GetStringMap(m.Name(), "loaded_modules", nil)
	for name, source := range loadedMods {
		if source == "local" {
			delete(loadedMods, name)
		}
	}
	if err := db.SetStringMap(m.Name(), "loaded_modules", loadedMods); err != nil {
		return fmt.Errorf("migrate local module manifest: %w", err)
	}
	if value, err := db.Get(m.Name(), "share_link", nil); err != nil {
		return fmt.Errorf("read obsolete Loader.share_link setting: %w", err)
	} else if value != nil {
		if err := db.Delete(m.Name(), "share_link"); err != nil {
			return fmt.Errorf("remove obsolete Loader.share_link setting: %w", err)
		}
	}
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

var _ goroku.ModuleWithConfigSchema = (*LoaderModule)(nil)

// ConfigSchema is the M7 typed config surface for Loader.
func (m *LoaderModule) ConfigSchema() []goroku.ConfigField {
	return []goroku.ConfigField{
		{Key: "MODULES_REPO", Type: "string", Default: "https://raw.githubusercontent.com/coddrago/modules/main", Validator: &goroku.StringValidator{}},
		{Key: "ADDITIONAL_REPOS", Type: "series", Default: []any{}, Validator: &goroku.SeriesValidator{}},
		{Key: "basic_auth", Type: "hidden", Default: "", Secret: true, Validator: &goroku.UnionValidator{Validators: []goroku.Validator{
			&goroku.NoneTypeValidator{},
			&goroku.RegExpValidator{Pattern: regexp.MustCompile(`^$`)},
			&goroku.RegExpValidator{Pattern: regexp.MustCompile(`^.*:.*$`)},
		}}},
		{Key: "command_emoji", Type: "string", Default: "<tg-emoji emoji-id=5197195523794157505>▫️</tg-emoji>", Validator: &goroku.StringValidator{}},
	}
}

func (m *LoaderModule) ConfigReady(config map[string]any) error {
	if val, ok := config["MODULES_REPO"].(string); ok {
		m.modulesRepo = val
	}
	if val, ok := config["basic_auth"].(string); ok {
		m.basicAuth = val
	}
	if val, ok := config["command_emoji"].(string); ok {
		m.commandEmoji = val
	}
	switch val := config["ADDITIONAL_REPOS"].(type) {
	case []any:
		m.additionalRepos = make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				m.additionalRepos = append(m.additionalRepos, s)
			}
		}
	case []string:
		m.additionalRepos = append([]string(nil), val...)
	}
	return nil
}

func (m *LoaderModule) ClientReady() error {
	m.beginModuleRestore()
	if m.db.GetBool("Loader", "secure_boot", false) {
		if err := m.db.SetBool("Loader", "secure_boot", false); err != nil {
			goroku.L().Error("clear secure boot flag", zap.Error(err))
		}
		go m.finishModuleRestore(true)
		return nil
	}
	go func() {
		// Restoring user modules runs their init/register code. A panic there
		// would otherwise take down the process and leave restoreDone unclosed,
		// blocking every WaitForRestore caller.
		fullyLoaded := false
		defer func() {
			if r := recover(); r != nil {
				goroku.L().Error("panic while restoring user modules", zap.Any("panic", r))
			}
			m.finishModuleRestore(fullyLoaded)
		}()
		loaded, err := m.restoreLoadedModules()
		if err != nil {
			goroku.L().Error("failed to restore user modules", zap.Error(err))
		}
		fullyLoaded = loaded
	}()
	return nil
}

func (m *LoaderModule) restoreLoadedModules() (bool, error) {
	var fullyLoaded bool
	err := withModuleTransaction(func() error {
		var restoreErr error
		fullyLoaded, restoreErr = m.restoreLoadedModulesLocked()
		return restoreErr
	})
	return fullyLoaded, err
}

func (m *LoaderModule) restoreLoadedModulesLocked() (bool, error) {
	loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)
	localMods := localRuntimeModules()
	for modName, path := range localMods {
		if _, tracked := loadedMods[modName]; !tracked {
			loadedMods[modName] = path
		}
	}
	if len(loadedMods) == 0 {
		return true, nil
	}

	loader := m.client.Loader
	if loader == nil {
		return false, nil
	}

	var structNames []string
	for modName, source := range loadedMods {
		path, pathErr := findInstalledModuleSource(modName)
		sourcePath, local := localMods[modName]
		if local {
			path, pathErr = sourcePath, nil
		}
		if pathErr != nil {
			path, pathErr = runtimeModuleSourcePath(modName)
			if pathErr != nil {
				continue
			}
		}
		bodyBytes, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
				if err := ensureRuntimeModuleSourceDir(); err != nil {
					continue
				}
				// Re-download must exactly match the digest recorded at installation.
				bodyBytes, err = m.restoreLoadedModule(modName, source, path)
				if err != nil {
					continue
				}
			} else {
				continue
			}
		} else if err := verifyModuleContentDigest(m.db, modName, bodyBytes, !local); err != nil {
			// Persisted source does not match its recorded digest.
			continue
		}
		structName := extractStructName(bodyBytes, modName)
		structNames = append(structNames, structName)
	}

	if len(structNames) == 0 {
		return false, nil
	}
	err := HotLoadStructs(loader, structNames)
	return err == nil, err
}

// localRuntimeModules derives local modules from their source files, so their
// installation state does not need to be duplicated in the database.
func localRuntimeModules() map[string]string {
	paths, err := filepath.Glob(filepath.Join(runtimeModuleSourceDir(), "*.go"))
	if err != nil {
		return nil
	}
	modules := make(map[string]string, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			continue
		}
		if names, parseErr := moduleStructNames(body); parseErr == nil && len(names) > 0 {
			modules[names[0]] = path
		}
	}
	return modules
}

func (m *LoaderModule) beginModuleRestore() {
	m.readyMu.Lock()
	m.fullyLoaded = false
	m.restoreComplete = false
	m.restoreDone = make(chan struct{})
	m.readyMu.Unlock()
}

func (m *LoaderModule) finishModuleRestore(fullyLoaded bool) {
	m.readyMu.Lock()
	m.fullyLoaded = fullyLoaded
	m.restoreComplete = true
	close(m.restoreDone)
	m.readyMu.Unlock()
}

func (m *LoaderModule) FullyLoaded() bool {
	m.readyMu.RLock()
	defer m.readyMu.RUnlock()
	return m.fullyLoaded
}

func (m *LoaderModule) RestoreComplete() bool {
	m.readyMu.RLock()
	defer m.readyMu.RUnlock()
	return m.restoreComplete
}

func (m *LoaderModule) WaitForRestore() <-chan struct{} {
	m.readyMu.RLock()
	done := m.restoreDone
	m.readyMu.RUnlock()
	if done != nil {
		return done
	}
	completed := make(chan struct{})
	close(completed)
	return completed
}

func (m *LoaderModule) restoreLoadedModule(modName, url, path string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(url), ".py") || strings.HasSuffix(strings.ToLower(modName), ".py") {
		return nil, fmt.Errorf("python module %s cannot be restored by Go loader", modName)
	}
	client := newModuleHTTPClient(10 * time.Second)
	if err := validateRemoteURL(url); err != nil {
		return nil, err
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("module restore failed with HTTP %d", resp.StatusCode)
	}
	body, err := utils.ReadResponseBodyLimited(resp, maxModuleSourceBytes)
	if err != nil {
		return nil, err
	}
	// Boot re-download must exactly match the digest recorded at installation.
	if err := verifyModuleContentDigest(m.db, modName, body, true); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
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
			Aliases:   []string{"lm"},
			OnlyOwner: true,
		},
		"unloadmod": {
			Aliases:   []string{"ulm"},
			OnlyOwner: true,
		},
		"dlmod": {
			Aliases:   []string{"dlm"},
			OnlyOwner: true,
		},
		"modload": {
			Aliases:   []string{"ml"},
			OnlyOwner: true,
		},
		"clearmodules": {
			OnlyOwner: true,
		},
		"addrepo": {
			OnlyOwner: true,
		},
		"delrepo": {
			OnlyOwner: true,
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

	if err := validateRemoteURL(url); err != nil {
		return nil, err
	}
	client := newModuleHTTPClient(5 * time.Second)
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	bodyBytes, err := utils.ReadResponseBodyLimited(resp, maxRepoIndexBytes)
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
		if validateRemoteURL(repo) != nil {
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
		im := m.client.GorokuInline
		if im != nil {
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
		return msg.Answer(msg.Text)
	}

	url := rawArgs
	var modName string
	sourceKind := moduleSourceURL

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		sourceKind = moduleSourceRepository
		msg.Text = m.getTrans("finding_module_in_repos", "<tg-emoji emoji-id=5873204392429096339>🔄</tg-emoji> Looking for modules in repositories...")
		if err := msg.Answer(msg.Text); err != nil {
			return err
		}

		foundURL, err := m.findLink(url)
		if err != nil {
			msg.Text = formatModuleInstallError(fmt.Errorf("module %q was not found in configured repositories: %w", rawArgs, err))
			return msg.Answer(msg.Text)
		}
		url = foundURL
		modName = rawArgs
	} else {
		msg.Text = m.getTrans("loading_module_via_file", "<tg-emoji emoji-id=5873204392429096339>🔄</tg-emoji> Loading the module...")
		if err := msg.Answer(msg.Text); err != nil {
			return err
		}
		fileName, parsedName := moduleFileAndName(url)
		if strings.HasSuffix(fileName, ".py") {
			msg.Text = formatModuleInstallError(errors.New("Python modules (.py) cannot be loaded; provide a native Go (.go) module"))
			return msg.Answer(msg.Text)
		}
		modName = parsedName
	}

	client := newModuleHTTPClient(5 * time.Second)
	bodyBytes, err := downloadModuleURL(client, url, maxModuleSourceBytes)
	if err != nil {
		msg.Text = formatModuleInstallError(fmt.Errorf("download failed: %w", err))
		return msg.Answer(msg.Text)
	}

	destPath, err := runtimeModuleSourcePath(modName)
	if err == nil {
		err = ensureRuntimeModuleSourceDir()
	}
	if err != nil {
		msg.Text = formatModuleInstallError(fmt.Errorf("prepare module storage: %w", err))
		return msg.Answer(msg.Text)
	}
	installed, err := m.installPersistedHotModule(msg, modName, destPath, url, bodyBytes)
	if err != nil && !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		msg.Text = formatModuleInstallError(err)
		return msg.Answer(msg.Text)
	}
	if installed == nil {
		msg.Text = formatModuleInstallError(errors.New("module was installed but could not be resolved in the runtime registry"))
		return msg.Answer(msg.Text)
	}
	source := sanitizedModuleSource(sourceKind, url)
	msg.Text = formatModuleInstalledCard(installed, moduleCommandPrefix(m.db, msg.SenderID), source, err, m.getTrans("loaded", defaultLoadedTemplate), m.commandEmoji, m.getTrans("undoc", "No docs"))
	return msg.Answer(msg.Text)
}

func (m *LoaderModule) LoadmodCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		if msg.ReplyToMsgID != 0 {
			replyMsg, err := msg.GetReplyMessage()
			if err == nil && replyMsg != nil && replyMsg.Media != nil {
				var buf bytes.Buffer
				err = m.client.DownloadMedia(replyMsg.Media, &buf)
				if err == nil {
					rawArgs = buf.String()
				}
			}
		}
	}

	if rawArgs == "" {
		msg.Text = m.getTrans("provide_module", "⚠️ <b>Provide a module to load</b>")
		return msg.Answer(msg.Text)
	}

	msg.Text = m.getTrans("loading_module_via_file", "<tg-emoji emoji-id=5873204392429096339>🔄</tg-emoji> Loading the module...")
	if err := msg.Answer(msg.Text); err != nil {
		return err
	}

	modName := "custom_module"
	isGo := true

	pyReg := regexp.MustCompile(`class\s+(\w+)\(loader\.Module\):`)

	if names, parseErr := moduleStructNames([]byte(rawArgs)); parseErr == nil && len(names) > 0 {
		modName = names[0]
		isGo = true
	} else if loc := pyReg.FindStringSubmatch(rawArgs); len(loc) == 2 {
		modName = loc[1]
		isGo = false
	}

	if !isGo {
		msg.Text = formatModuleInstallError(errors.New("Python modules (.py) cannot be loaded; provide a native Go (.go) module"))
		return msg.Answer(msg.Text)
	}

	destPath, err := runtimeModuleSourcePath(modName)
	if err == nil {
		err = ensureRuntimeModuleSourceDir()
	}
	var installed goroku.Module
	if err == nil {
		installed, err = m.installPersistedHotModule(msg, modName, destPath, "local", []byte(rawArgs))
	}
	if err != nil && !errors.Is(err, goroku.ErrDatabaseCommitUncertain) {
		msg.Text = formatModuleInstallError(err)
		return msg.Answer(msg.Text)
	}
	if installed == nil {
		msg.Text = formatModuleInstallError(errors.New("module was installed but could not be resolved in the runtime registry"))
		return msg.Answer(msg.Text)
	}
	msg.Text = formatModuleInstalledCard(installed, moduleCommandPrefix(m.db, msg.SenderID), string(moduleSourceLocal), err, m.getTrans("loaded", defaultLoadedTemplate), m.commandEmoji, m.getTrans("undoc", "No docs"))
	return msg.Answer(msg.Text)
}

func (m *LoaderModule) UnloadmodCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		msg.Text = m.getTrans("no_class", "<b>What class needs to be unloaded?</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	modName := strings.ToLower(rawArgs)
	loader := m.client.Loader
	if loader == nil {
		msg.Text = "❌ Modules registry not found."
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)

	// Check if this is a system module (statically registered, not in loaded_modules)
	var matchedKey string
	isSystem := true
	for k := range loadedMods {
		kClean := strings.ReplaceAll(strings.ToLower(k), "module", "")
		if strings.ToLower(k) == modName || kClean == modName {
			isSystem = false
			matchedKey = k
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
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}
	if module := loader.LookupByName(foundName); module != nil {
		if _, err := findRegisteredModuleSource(module); err == nil {
			isSystem = false
			matchedKey = foundName
		}
	}

	if isSystem {
		msg.Text = formatTrans(m.getTrans("system_unload_forbidden", "🚫 <b>Module {} is a system module and cannot be unloaded.</b>"), foundName)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	err := m.uninstallPersistedHotModule(msg, matchedKey, foundName)
	if err != nil {
		msg.Text = moduleTransactionReport("Module uninstall", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	err = m.unregisterHotLoad(msg, foundName)
	if err != nil {
		msg.Text = fmt.Sprintf("❌ <b>Unload failed:</b> %v", err)
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
	}

	return nil
}

func (m *LoaderModule) ClearmodulesCmd(msg *goroku.Message) error {
	im := m.client.GorokuInline
	if im == nil {
		return m.executeClearModules(msg)
	}

	confirmText := m.getTrans("confirm_clearmodules", "⚠️ <b>Are you sure you want to clear all modules?</b>")
	markup := [][]inline.Button{
		{
			{
				Text: m.getTrans("clearmodules", "🗑 Clear modules"),
				Data: "clear_mods_confirm",
				Handler: func(c inline.CallbackQuery) error {
					if !requireOwnerCallback(m.client, c, c.FromID) {
						return nil
					}
					_ = c.Answer("Deleting modules...", false)
					_ = closeForm(c)
					loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)

					for modName := range loadedMods {
						if path, pathErr := runtimeModuleSourcePath(modName); pathErr == nil {
							_ = os.Remove(path)
						}
					}
					for _, path := range localRuntimeModules() {
						_ = os.Remove(path)
					}

					if err := m.db.Update(map[string]map[string]any{
						"Loader": {
							"loaded_modules": make(map[string]string),
							moduleDigestsKey: make(map[string]string),
						},
					}); err != nil {
						return err
					}

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
					return closeForm(c)
				},
			},
		},
	}

	_, err := im.Form(confirmText, msg.ChatID, markup, inline.WithForceMe(true))
	return err
}

func (m *LoaderModule) executeClearModules(msg *goroku.Message) error {
	loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)

	for modName := range loadedMods {
		if path, pathErr := runtimeModuleSourcePath(modName); pathErr == nil {
			_ = os.Remove(path)
		}
	}
	for _, path := range localRuntimeModules() {
		_ = os.Remove(path)
	}

	if err := m.db.Update(map[string]map[string]any{
		"Loader": {
			"loaded_modules": make(map[string]string),
			moduleDigestsKey: make(map[string]string),
		},
	}); err != nil {
		return err
	}

	msg.Text = m.getTrans("all_modules_deleted", "✅ All modules deleted")
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
	}

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
	return nil
}

func (m *LoaderModule) AddrepoCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" || validateRemoteURL(rawArgs) != nil {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	rawArgs = strings.TrimSuffix(rawArgs, "/")

	client := newModuleHTTPClient(5 * time.Second)
	if _, err := downloadModuleURL(client, fmt.Sprintf("%s/full.txt", rawArgs), maxRepoIndexBytes); err != nil {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

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
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	additionalRepos := append(append([]string(nil), m.additionalRepos...), rawArgs)
	if err := m.db.Set("Loader", "ADDITIONAL_REPOS", additionalRepos); err != nil {
		return err
	}
	m.additionalRepos = additionalRepos

	msg.Text = formatTrans(m.getTrans("repo_added", "✅ <b>Repository {} added</b>"), rawArgs)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
	}
	return nil
}

func (m *LoaderModule) DelrepoCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" || (!strings.HasPrefix(rawArgs, "http://") && !strings.HasPrefix(rawArgs, "https://")) {
		msg.Text = m.getTrans("no_repo", "🚫 <b>Invalid repository URL</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
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
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	additionalRepos := append([]string(nil), m.additionalRepos...)
	additionalRepos = append(additionalRepos[:idx], additionalRepos[idx+1:]...)
	if err := m.db.Set("Loader", "ADDITIONAL_REPOS", additionalRepos); err != nil {
		return err
	}
	m.additionalRepos = additionalRepos

	msg.Text = formatTrans(m.getTrans("repo_deleted", "✅ <b>Repository {} deleted</b>"), rawArgs)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
	}
	return nil
}

func (m *LoaderModule) ModloadCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	if rawArgs == "" {
		msg.Text = m.getTrans("args", "🚫 <b>You must specify arguments</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	loader := m.client.Loader
	if loader == nil {
		msg.Text = "❌ Modules registry not found."
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	modulesList := loader.GetModules()
	var foundMod goroku.Module
	var class_name string

	for name, mod := range modulesList {
		if strings.EqualFold(name, rawArgs) || strings.EqualFold(mod.Name(), rawArgs) || strings.EqualFold(registeredModuleStructName(mod), rawArgs) {
			foundMod = mod
			class_name = name
			break
		}
	}

	if foundMod == nil {
		for name, mod := range modulesList {
			structName := registeredModuleStructName(mod)
			if strings.Contains(strings.ToLower(name), strings.ToLower(rawArgs)) || strings.Contains(strings.ToLower(mod.Name()), strings.ToLower(rawArgs)) || strings.Contains(strings.ToLower(structName), strings.ToLower(rawArgs)) {
				foundMod = mod
				class_name = name
				break
			}
		}
	}

	if foundMod == nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	path, err := findModuleSourceForExport(foundMod)
	if err != nil {
		path, err = findModuleSource(class_name)
	}
	if err != nil {
		path, err = findModuleSource(foundMod.Name())
	}

	if err != nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	fileBytes, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		msg.Text = m.getTrans("404", "🚫 <b>Module not found</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(goroku.ChatRefID(msg.ChatID), msg.ID, msg.Text)
		}
		return nil
	}

	prefix := m.db.GetString("goroku.main", "command_prefix", ".")

	loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)

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
		_, err = m.client.SendFileWithOptions(goroku.ChatRefID(msg.ChatID), nr, text, opts...)
		return err
	}

	return nil
}

func (m *LoaderModule) installHotModule(msg *goroku.Message, fallbackName, destPath string, body []byte) error {
	if m.client == nil || m.client.Loader == nil {
		return fmt.Errorf("modules registry not found")
	}
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".module-*.go")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	structName := extractStructName(body, fallbackName)
	started := time.Now()
	digest := contentSHA256(body)
	// Native Go plugins cannot be fully unloaded from process memory after plugin.Open.
	prepared, err := prepareHotModule(structName, tmpPath)
	auditExecution(executionAuditEvent{
		ActorID:    messageActorID(msg),
		ChatID:     messageChatID(msg),
		Capability: "plugin.install",
		Digest:     digest,
		Duration:   time.Since(started),
		ExitCode:   auditExitCode(err),
		Status:     auditStatus(err, false, false),
	})
	if err != nil {
		return fmt.Errorf("module preparation failed: %w", err)
	}
	if err := rejectSelfModuleTransaction(msg, m.client.Loader, prepared.Name(), "replace"); err != nil {
		return err
	}

	oldBody, readErr := os.ReadFile(destPath) //nolint:gosec
	hadOld := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	if err := replacePreparedHotModule(m.client.Loader, prepared); err != nil {
		var rollbackErr error
		if hadOld {
			rollbackErr = atomicWriteFile(destPath, oldBody)
		} else {
			if removeErr := os.Remove(destPath); removeErr != nil && !os.IsNotExist(removeErr) {
				rollbackErr = removeErr
			}
		}
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("hot-load failed: %w", err), fmt.Errorf("source rollback failed: %w", rollbackErr))
		}
		return fmt.Errorf("hot-load failed: %w", err)
	}
	return nil
}

func (m *LoaderModule) installPersistedHotModule(msg *goroku.Message, modName, destPath, provenance string, body []byte) (goroku.Module, error) {
	installedName := extractStructName(body, modName)
	if err := rejectSelfModuleTransaction(msg, m.client.Loader, installedName, "replace"); err != nil {
		return nil, err
	}
	var installedModule goroku.Module
	err := withModuleTransaction(func() error {
		oldBody, readErr := os.ReadFile(destPath) //nolint:gosec
		hadOldSource := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		oldModules := m.client.Loader.GetModules()

		if m.installHotModuleApply != nil {
			if err := m.installHotModuleApply(msg, modName, destPath, body); err != nil {
				return err
			}
		} else if err := m.installHotModule(msg, modName, destPath, body); err != nil {
			return err
		}
		oldModule := oldModules[strings.ToLower(installedName)]
		if current := m.client.Loader.LookupByName(installedName); current == nil || current == oldModule {
			for name, current := range m.client.Loader.GetModules() {
				if oldModules[name] != current {
					installedName = current.Name()
					oldModule = oldModules[name]
					break
				}
			}
		}
		installedModule = m.client.Loader.LookupByName(installedName)
		loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)
		if provenance == "local" {
			delete(loadedMods, modName)
		} else {
			loadedMods[modName] = provenance
		}
		digest := contentSHA256(body)
		digests := moduleContentDigests(m.db)
		digests[modName] = digest
		setLoadedModules := func(modules map[string]string) error {
			return m.db.SetStringMap("Loader", "loaded_modules", modules)
		}
		if m.setLoadedModulesApply != nil {
			setLoadedModules = m.setLoadedModulesApply
		}
		var dbErr error
		if m.setLoadedModulesApply != nil {
			dbErr = setLoadedModules(loadedMods)
			if dbErr == nil {
				_ = setModuleContentDigest(m.db, modName, digest)
			}
		} else {
			dbErr = m.db.Update(map[string]map[string]any{
				"Loader": {
					"loaded_modules": loadedMods,
					moduleDigestsKey: digests,
				},
			})
		}
		if dbErr == nil {
			return nil
		}
		if errors.Is(dbErr, goroku.ErrDatabaseCommitUncertain) {
			return fmt.Errorf("module manifest committed with durability warning: %w", dbErr)
		}

		var rollbackErr error
		if hadOldSource {
			rollbackErr = atomicWriteFile(destPath, oldBody)
		} else if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
			rollbackErr = err
		}
		if current := m.client.Loader.LookupByName(installedName); current != nil {
			if _, err := unloadModuleForTransaction(m.client.Loader, current.Name()); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		if oldModule != nil {
			if err := registerRestoredModule(m.client.Loader, oldModule); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("database update failed: %w", dbErr), fmt.Errorf("module rollback failed: %w", rollbackErr))
		}
		return fmt.Errorf("database update failed: %w", dbErr)
	})
	return installedModule, err
}

func (m *LoaderModule) uninstallPersistedHotModule(msg *goroku.Message, modName, registeredName string) error {
	if err := rejectSelfModuleTransaction(msg, m.client.Loader, registeredName, "uninstall"); err != nil {
		return err
	}
	return withModuleTransaction(func() error {
		loadedMods := m.db.GetStringMap("Loader", "loaded_modules", nil)
		oldModule := m.client.Loader.LookupByName(registeredName)
		if oldModule == nil {
			return fmt.Errorf("module %s not found", registeredName)
		}
		destPath, err := findRegisteredModuleSource(oldModule)
		if err != nil {
			destPath, err = runtimeModuleSourcePath(modName)
			if err != nil {
				return err
			}
		}
		oldBody, readErr := os.ReadFile(destPath) //nolint:gosec
		hadSource := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}

		detached, err := unloadModuleForTransaction(m.client.Loader, oldModule.Name())
		if err != nil {
			if detached {
				return restoreDetachedModule(m.client.Loader, oldModule, fmt.Errorf("unload module %s: %w", oldModule.Name(), err))
			}
			return err
		}
		if hadSource {
			if err := os.Remove(destPath); err != nil {
				return restoreDetachedModule(m.client.Loader, oldModule, err)
			}
		}
		delete(loadedMods, modName)
		digests := moduleContentDigests(m.db)
		delete(digests, modName)
		setLoadedModules := func(modules map[string]string) error {
			return m.db.SetStringMap("Loader", "loaded_modules", modules)
		}
		if m.setLoadedModulesApply != nil {
			setLoadedModules = m.setLoadedModulesApply
		}
		var dbErr error
		if m.setLoadedModulesApply != nil {
			dbErr = setLoadedModules(loadedMods)
			if dbErr == nil {
				_ = clearModuleContentDigest(m.db, modName)
			}
		} else {
			dbErr = m.db.Update(map[string]map[string]any{
				"Loader": {
					"loaded_modules": loadedMods,
					moduleDigestsKey: digests,
				},
			})
		}
		if dbErr == nil {
			return nil
		}
		if errors.Is(dbErr, goroku.ErrDatabaseCommitUncertain) {
			return fmt.Errorf("module manifest committed with durability warning: %w", dbErr)
		}

		var rollbackErr error
		if hadSource {
			rollbackErr = atomicWriteFile(destPath, oldBody)
		}
		if err := registerRestoredModule(m.client.Loader, oldModule); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("database update failed: %w", dbErr), fmt.Errorf("module rollback failed: %w", rollbackErr))
		}
		return fmt.Errorf("database update failed: %w", dbErr)
	})
}

func messageActorID(msg *goroku.Message) int64 {
	if msg == nil {
		return 0
	}
	return msg.SenderID
}

func messageChatID(msg *goroku.Message) int64 {
	if msg == nil {
		return 0
	}
	return msg.ChatID
}

func auditExitCode(err error) int {
	if err == nil {
		return 0
	}
	return -1
}

func atomicWriteFile(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rollback-*.go")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (m *LoaderModule) unregisterHotLoad(msg *goroku.Message, structName string) error {
	trans := m.getTrans("unloaded", "{} <b>Module {} unloaded.</b>")
	trans = strings.Replace(trans, "{}", "<tg-emoji emoji-id=5784993237412351403>✅</tg-emoji>", 1)
	trans = strings.Replace(trans, "{}", structName, 1)
	_ = msg.Answer(trans)
	return nil
}
