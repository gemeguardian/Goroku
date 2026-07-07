package modules

import (
	"crypto/md5" //nolint:gosec
	"encoding/hex"
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

func (m *GorokuPluginSecurity) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuPluginSecurity) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

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

func (m *GorokuPluginSecurity) pluginStringSlice(key string) []string {
	raw, _ := m.db.Get("GorokuPluginSecurity", key, []any{})
	switch slice := raw.(type) {
	case []string:
		return append([]string(nil), slice...)
	case []any:
		result := make([]string, 0, len(slice))
		for _, item := range slice {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
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
	internalized := m.pluginStringSlice("internalized")
	alreadyInternal := stringIndex(internalized, modName, true) != -1
	if !alreadyInternal {
		internalized = append(internalized, modName)
		m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized)
	}

	// 2. Allow session
	sessionAllow := m.pluginStringSlice("session_allow")
	alreadyAllowed := stringIndex(sessionAllow, modHash, false) != -1
	if !alreadyAllowed {
		sessionAllow = append(sessionAllow, modHash)
		m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow)
	}

	var respText string
	if !alreadyInternal {
		template := m.getTrans("external_removed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external снят для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		template := m.getTrans("already_internal", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Модуль</b> <code>{0}</code> <b>уже внутренний</b>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) ExternalCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	// 1. Externalize (untrust)
	internalized := m.pluginStringSlice("internalized")
	foundIntIdx := stringIndex(internalized, modName, true)
	if foundIntIdx != -1 {
		internalized = removeStringAt(internalized, foundIntIdx)
		m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized)
	}

	// 2. Deny session
	sessionAllow := m.pluginStringSlice("session_allow")
	foundSessIdx := stringIndex(sessionAllow, modHash, false)
	if foundSessIdx != -1 {
		sessionAllow = removeStringAt(sessionAllow, foundSessIdx)
		m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow)
	}

	var respText string
	if foundIntIdx != -1 {
		template := m.getTrans("external_restored", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external возвращён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		template := m.getTrans("already_external", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Модуль</b> <code>{0}</code> <b>уже внешний</b>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) AllowmodCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	sessionAllow := m.pluginStringSlice("session_allow")
	alreadyAllowed := stringIndex(sessionAllow, modHash, false) != -1

	var respText string
	if alreadyAllowed {
		template := m.getTrans("already_allowed", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = append(sessionAllow, modHash)
		m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow)
		template := m.getTrans("session_allowed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) DenymodCmd(msg *goroku.Message) error {
	_, modName, modHash, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	sessionAllow := m.pluginStringSlice("session_allow")
	foundIdx := stringIndex(sessionAllow, modHash, false)

	var respText string
	if foundIdx == -1 {
		template := m.getTrans("already_denied", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = removeStringAt(sessionAllow, foundIdx)
		m.db.SetStringSlice("GorokuPluginSecurity", "session_allow", sessionAllow)
		template := m.getTrans("session_denied", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) TrustmodCmd(msg *goroku.Message) error {
	_, modName, _, ok := m.resolveModuleArg(msg)
	if !ok {
		return nil
	}

	internalized := m.pluginStringSlice("internalized")
	foundIdx := stringIndex(internalized, modName, true)

	var respText string
	if foundIdx != -1 {
		// Untrust (make external)
		internalized = removeStringAt(internalized, foundIdx)
		m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized)
		template := m.getTrans("external_restored", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external возвращён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		// Trust (make internalized)
		internalized = append(internalized, modName)
		m.db.SetStringSlice("GorokuPluginSecurity", "internalized", internalized)
		template := m.getTrans("external_removed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external снят для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}
