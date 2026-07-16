package modules

import (
	"crypto/md5" //nolint:gosec
	"encoding/hex"
	"fmt"
	"goroku/goroku"
	"strings"
)

type GorokuPluginSecurity struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *GorokuPluginSecurity) Name() string {
	return "GorokuPluginSecurity"
}

func (m *GorokuPluginSecurity) Strings() map[string]string {
	return map[string]string{
		"name": "Goroku Plugin Security Module",
	}
}

func (m *GorokuPluginSecurity) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *GorokuPluginSecurity) ClientReady() error { return nil }
func (m *GorokuPluginSecurity) OnUnload() error    { return nil }
func (m *GorokuPluginSecurity) OnDlmod() error     { return nil }

func (m *GorokuPluginSecurity) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"unexternal": m.UnexternalCmd,
		"external":   m.ExternalCmd,
		"allowmod":   m.AllowmodCmd,
		"denymod":    m.DenymodCmd,
		"trustmod":   m.TrustmodCmd,
	}
}

func (m *GorokuPluginSecurity) CommandMetas() map[string]goroku.CommandMeta {
	// Trust/external/allow are dangerous capability controls (M4.3).
	return map[string]goroku.CommandMeta{
		"unexternal": {OnlyOwner: true},
		"external":   {OnlyOwner: true},
		"allowmod":   {OnlyOwner: true},
		"denymod":    {OnlyOwner: true},
		"trustmod":   {OnlyOwner: true},
	}
}

func (m *GorokuPluginSecurity) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuPluginSecurity) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

// getModuleHash returns the legacy MD5(name) identifier used by older
// session_allow entries. Prefer content SHA-256 digests (see trusted_digests)
// for install/trust decisions; name hashes remain secondary for resolve UX.
func getModuleHash(name string) string {
	hasher := md5.New() //nolint:gosec
	hasher.Write([]byte(strings.ToLower(name)))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (m *GorokuPluginSecurity) resolveModule(query string) (goroku.Module, string) {
	loader := m.client.Loader
	if loader == nil {
		return nil, ""
	}
	modules := loader.GetModules()
	query = strings.ToLower(query)
	var closest string
	minDist := 9999

	for _, mod := range modules {
		nameL := strings.ToLower(mod.Name())
		if nameL == query || getModuleHash(mod.Name()) == query {
			return mod, ""
		}
		dist := editDistance(query, nameL)
		if dist < minDist {
			minDist = dist
			closest = mod.Name()
		}
	}
	if minDist <= 3 {
		return nil, closest
	}
	return nil, ""
}

func editDistance(s, t string) int {
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(s); i++ {
		for j := 1; j <= len(t); j++ {
			cost := 1
			if s[i-1] == t[j-1] {
				cost = 0
			}
			d[i][j] = minVal(
				d[i-1][j]+1,
				minVal(
					d[i][j-1]+1,
					d[i-1][j-1]+cost,
				),
			)
		}
	}
	return d[len(s)][len(t)]
}

func minVal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *GorokuPluginSecurity) resolveModuleArg(msg *goroku.Message) (goroku.Module, string, string, bool) {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil, "", "", false
	}

	mod, closest := m.resolveModule(strings.TrimSpace(parts[1]))
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil, "", "", false
	}

	modName := mod.Name()
	return mod, modName, getModuleHash(modName), true
}

func (m *GorokuPluginSecurity) pluginStringSlice(key string) ([]string, error) {
	raw, err := m.db.Get("GorokuPluginSecurity", key, []any{})
	if err != nil {
		return nil, err
	}
	switch slice := raw.(type) {
	case []string:
		return append([]string(nil), slice...), nil
	case []any:
		result := make([]string, 0, len(slice))
		for _, item := range slice {
			if s, ok := item.(string); ok {
				result = append(result, s)
			} else {
				return nil, fmt.Errorf("GorokuPluginSecurity %s contains type %T, want string", key, item)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("GorokuPluginSecurity %s has type %T, want string slice", key, raw)
	}
}

func stringIndex(items []string, needle string, fold bool) int {
	for i, item := range items {
		if (fold && strings.EqualFold(item, needle)) || (!fold && item == needle) {
			return i
		}
	}
	return -1
}

func removeStringAt(items []string, idx int) []string {
	return append(items[:idx], items[idx+1:]...)
}

func (m *GorokuPluginSecurity) UnexternalCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	// 1. Internalize (trust)
	internalized, err := m.pluginStringSlice("internalized")
	if err != nil {
		return err
	}
	alreadyInternal := stringIndex(internalized, modName, true) != -1
	if !alreadyInternal {
		internalized = append(internalized, modName)
	}

	// 2. Allow session
	sessionAllow, err := m.pluginStringSlice("session_allow")
	if err != nil {
		return err
	}
	alreadyAllowed := stringIndex(sessionAllow, modHash, false) != -1
	if !alreadyAllowed {
		sessionAllow = append(sessionAllow, modHash)
	}
	if !alreadyInternal || !alreadyAllowed {
		if err := m.db.Update(map[string]map[string]any{
			"GorokuPluginSecurity": {
				"internalized":  internalized,
				"session_allow": sessionAllow,
			},
		}); err != nil {
			return err
		}
	}
	_ = trustInstalledModuleContent(m.db, modName)

	var respText string
	if !alreadyInternal {
		template := m.getTrans("external_removed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external снят для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		template := m.getTrans("already_internal", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Модуль</b> <code>{0}</code> <b>уже внутренний</b>")
		respText = formatTrans(template, modName)
	}

	return msg.Answer(respText)
}

func (m *GorokuPluginSecurity) ExternalCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	// 1. Externalize (untrust)
	internalized, err := m.pluginStringSlice("internalized")
	if err != nil {
		return err
	}
	foundIntIdx := stringIndex(internalized, modName, true)
	if foundIntIdx != -1 {
		internalized = removeStringAt(internalized, foundIntIdx)
	}

	// 2. Deny session
	sessionAllow, err := m.pluginStringSlice("session_allow")
	if err != nil {
		return err
	}
	foundSessIdx := stringIndex(sessionAllow, modHash, false)
	if foundSessIdx != -1 {
		sessionAllow = removeStringAt(sessionAllow, foundSessIdx)
	}
	if foundIntIdx != -1 || foundSessIdx != -1 {
		if err := m.db.Update(map[string]map[string]any{
			"GorokuPluginSecurity": {
				"internalized":  internalized,
				"session_allow": sessionAllow,
			},
		}); err != nil {
			return err
		}
	}
	_ = untrustInstalledModuleContent(m.db, modName)

	var respText string
	if foundIntIdx != -1 {
		template := m.getTrans("external_restored", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external возвращён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		template := m.getTrans("already_external", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Модуль</b> <code>{0}</code> <b>уже внешний</b>")
		respText = formatTrans(template, modName)
	}

	return msg.Answer(respText)
}

func (m *GorokuPluginSecurity) AllowmodCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	sessionAllow, err := m.pluginStringSlice("session_allow")
	if err != nil {
		return err
	}
	alreadyAllowed := stringIndex(sessionAllow, modHash, false) != -1

	var respText string
	if alreadyAllowed {
		template := m.getTrans("already_allowed", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = append(sessionAllow, modHash)
		if err := m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow); err != nil {
			return err
		}
		// Also trust content digest when installed source is available.
		_ = trustInstalledModuleContent(m.db, modName)
		template := m.getTrans("session_allowed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	return msg.Answer(respText)
}

func (m *GorokuPluginSecurity) DenymodCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	sessionAllow, err := m.pluginStringSlice("session_allow")
	if err != nil {
		return err
	}
	foundIdx := stringIndex(sessionAllow, modHash, false)

	var respText string
	if foundIdx == -1 {
		template := m.getTrans("already_denied", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = removeStringAt(sessionAllow, foundIdx)
		if err := m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow); err != nil {
			return err
		}
		_ = untrustInstalledModuleContent(m.db, modName)
		template := m.getTrans("session_denied", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	return msg.Answer(respText)
}

func (m *GorokuPluginSecurity) TrustmodCmd(msg *goroku.Message) error {
	_, modName, _, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	internalized, err := m.pluginStringSlice("internalized")
	if err != nil {
		return err
	}
	foundIdx := stringIndex(internalized, modName, true)

	var respText string
	if foundIdx != -1 {
		// Untrust (make external) and drop content digest allowlist entry.
		internalized = removeStringAt(internalized, foundIdx)
		if err := m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized); err != nil {
			return err
		}
		_ = untrustInstalledModuleContent(m.db, modName)
		template := m.getTrans("external_restored", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external возвращён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		// Trust (make internalized) + content digest allowlist when source exists.
		internalized = append(internalized, modName)
		if err := m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized); err != nil {
			return err
		}
		_ = trustInstalledModuleContent(m.db, modName)
		template := m.getTrans("external_removed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external снят для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	return msg.Answer(respText)
}
