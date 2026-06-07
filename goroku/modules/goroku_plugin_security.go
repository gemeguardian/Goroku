package modules

import (
	"crypto/md5"
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
	hasher := md5.New()
	hasher.Write([]byte(strings.ToLower(name)))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (m *GorokuPluginSecurity) resolveModule(query string) (goroku.Module, string) {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
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

func (m *GorokuPluginSecurity) UnexternalCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil
	}
	query := strings.TrimSpace(parts[1])
	mod, closest := m.resolveModule(query)
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil
	}

	modName := mod.Name()
	modHash := getModuleHash(modName)

	// 1. Internalize (trust)
	rawInternalized := m.db.Get("GorokuPluginSecurity", "internalized", []interface{}{})
	var internalized []string
	alreadyInternal := false
	if slice, ok := rawInternalized.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				internalized = append(internalized, s)
				if strings.ToLower(s) == strings.ToLower(modName) {
					alreadyInternal = true
				}
			}
		}
	}
	if !alreadyInternal {
		internalized = append(internalized, modName)
		m.db.Set("GorokuPluginSecurity", "internalized", internalized)
	}

	// 2. Allow session
	rawSession := m.db.Get("GorokuPluginSecurity", "session_allow", []interface{}{})
	var sessionAllow []string
	alreadyAllowed := false
	if slice, ok := rawSession.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				sessionAllow = append(sessionAllow, s)
				if s == modHash {
					alreadyAllowed = true
				}
			}
		}
	}
	if !alreadyAllowed {
		sessionAllow = append(sessionAllow, modHash)
		m.db.Set("GorokuPluginSecurity", "session_allow", sessionAllow)
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
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil
	}
	query := strings.TrimSpace(parts[1])
	mod, closest := m.resolveModule(query)
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil
	}

	modName := mod.Name()
	modHash := getModuleHash(modName)

	// 1. Externalize (untrust)
	rawInternalized := m.db.Get("GorokuPluginSecurity", "internalized", []interface{}{})
	var internalized []string
	foundIntIdx := -1
	if slice, ok := rawInternalized.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				internalized = append(internalized, s)
				if strings.ToLower(s) == strings.ToLower(modName) {
					foundIntIdx = len(internalized) - 1
				}
			}
		}
	}
	if foundIntIdx != -1 {
		internalized = append(internalized[:foundIntIdx], internalized[foundIntIdx+1:]...)
		m.db.Set("GorokuPluginSecurity", "internalized", internalized)
	}

	// 2. Deny session
	rawSession := m.db.Get("GorokuPluginSecurity", "session_allow", []interface{}{})
	var sessionAllow []string
	foundSessIdx := -1
	if slice, ok := rawSession.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				sessionAllow = append(sessionAllow, s)
				if s == modHash {
					foundSessIdx = len(sessionAllow) - 1
				}
			}
		}
	}
	if foundSessIdx != -1 {
		sessionAllow = append(sessionAllow[:foundSessIdx], sessionAllow[foundSessIdx+1:]...)
		m.db.Set("GorokuPluginSecurity", "session_allow", sessionAllow)
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
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil
	}
	query := strings.TrimSpace(parts[1])
	mod, closest := m.resolveModule(query)
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil
	}

	modName := mod.Name()
	modHash := getModuleHash(modName)

	raw := m.db.Get("GorokuPluginSecurity", "session_allow", []interface{}{})
	var sessionAllow []string
	alreadyAllowed := false
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				sessionAllow = append(sessionAllow, s)
				if s == modHash {
					alreadyAllowed = true
				}
			}
		}
	}

	var respText string
	if alreadyAllowed {
		template := m.getTrans("already_allowed", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = append(sessionAllow, modHash)
		m.db.Set("GorokuPluginSecurity", "session_allow", sessionAllow)
		template := m.getTrans("session_allowed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session разрешён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) DenymodCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil
	}
	query := strings.TrimSpace(parts[1])
	mod, closest := m.resolveModule(query)
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil
	}

	modName := mod.Name()
	modHash := getModuleHash(modName)

	raw := m.db.Get("GorokuPluginSecurity", "session_allow", []interface{}{})
	var sessionAllow []string
	foundIdx := -1
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				sessionAllow = append(sessionAllow, s)
				if s == modHash {
					foundIdx = len(sessionAllow) - 1
				}
			}
		}
	}

	var respText string
	if foundIdx == -1 {
		template := m.getTrans("already_denied", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Доступ к .session уже запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		sessionAllow = append(sessionAllow[:foundIdx], sessionAllow[foundIdx+1:]...)
		m.db.Set("GorokuPluginSecurity", "session_allow", sessionAllow)
		template := m.getTrans("session_denied", "<emoji document_id=5118861066981344121>✅</emoji> <b>Доступ к .session запрещён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}

func (m *GorokuPluginSecurity) TrustmodCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		_ = msg.Answer(m.getTrans("no_hash", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Нужно указать название модуля</b>"))
		return nil
	}
	query := strings.TrimSpace(parts[1])
	mod, closest := m.resolveModule(query)
	if mod == nil {
		if closest != "" {
			template := m.getTrans("hash_not_found_suggest", "<emoji document_id=5312383351217201533>⚠️</emoji> <b>Совпадений нет. Ближайшее:</b> <code>{0}</code>")
			_ = msg.Answer(formatTrans(template, closest))
		} else {
			_ = msg.Answer(m.getTrans("hash_not_found", "<emoji document_id=5210952531676504517>🚫</emoji> <b>Модуль с таким названием не найден</b>"))
		}
		return nil
	}

	modName := mod.Name()
	raw := m.db.Get("GorokuPluginSecurity", "internalized", []interface{}{})
	var internalized []string
	foundIdx := -1
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			if s, ok := item.(string); ok {
				internalized = append(internalized, s)
				if strings.ToLower(s) == strings.ToLower(modName) {
					foundIdx = len(internalized) - 1
				}
			}
		}
	}

	var respText string
	if foundIdx != -1 {
		// Untrust (make external)
		internalized = append(internalized[:foundIdx], internalized[foundIdx+1:]...)
		m.db.Set("GorokuPluginSecurity", "internalized", internalized)
		template := m.getTrans("external_restored", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external возвращён для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	} else {
		// Trust (make internalized)
		internalized = append(internalized, modName)
		m.db.Set("GorokuPluginSecurity", "internalized", internalized)
		template := m.getTrans("external_removed", "<emoji document_id=5118861066981344121>✅</emoji> <b>Флаг is_external снят для</b> <code>{0}</code>")
		respText = formatTrans(template, modName)
	}

	_ = msg.Answer(respText)
	return nil
}
