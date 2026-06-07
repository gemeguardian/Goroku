package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/gotd/td/tg"
)

type GorokuSecurity struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *GorokuSecurity) Name() string {
	return "GorokuSecurity"
}

func (m *GorokuSecurity) Strings() map[string]string {
	return map[string]string{
		"name": "Goroku Security Module",
	}
}

func (m *GorokuSecurity) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *GorokuSecurity) ClientReady() error {
	m.applyGroupsToManager()
	return nil
}
func (m *GorokuSecurity) OnUnload() error { return nil }
func (m *GorokuSecurity) OnDlmod() error  { return nil }

func (m *GorokuSecurity) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"owner":     m.OwnerCmd,
		"ownerlist": m.OwnerCmd,
		"addowner":  m.AddownerCmd,
		"owneradd":  m.AddownerCmd,
		"delowner":  m.DelownerCmd,
		"ownerrm":   m.DelownerCmd,
		"sudo":      m.SudoCmd,
		"sudolist":  m.SudoCmd,
		"addsudo":   m.AddsudoCmd,
		"sudoadd":   m.AddsudoCmd,
		"delsudo":   m.DelsudoCmd,
		"sudorm":    m.DelsudoCmd,
		"security":  m.SecurityCmd,
		"tsec":      m.TsecCmd,
		"ttsec":     m.TtsecCmd,
		"tsecrm":    m.TtsecCmd,
		"tsecclr":   m.TtsecCmd,
		"newsgroup": m.NewsgroupCmd,
		"sgroups":   m.SgroupsCmd,
		"sgroup":    m.SgroupCmd,
		"delsgroup": m.DelsgroupCmd,
		"sgroupadd": m.SgroupaddCmd,
		"sgroupdel": m.SgroupdelCmd,
	}
}

func (m *GorokuSecurity) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuSecurity) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

// securityGroup holds a named group of users with permissions.
type securityGroup struct {
	Users       []int64                  `json:"users"`
	Permissions []map[string]interface{} `json:"permissions"`
}

func (m *GorokuSecurity) loadGroups() map[string]securityGroup {
	raw := m.db.Get("goroku.security", "sgroups", nil)
	if raw == nil {
		raw = m.db.Get("goroku.security", "security_groups", map[string]interface{}{})
	}
	result := make(map[string]securityGroup)

	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return result
	}

	for name, val := range rawMap {
		bytes, err := json.Marshal(val)
		if err != nil {
			continue
		}
		var sg securityGroup
		if err := json.Unmarshal(bytes, &sg); err != nil {
			continue
		}
		result[name] = sg
	}
	return result
}

func (m *GorokuSecurity) saveGroups(groups map[string]securityGroup) {
	out := make(map[string]interface{}, len(groups))
	for k, v := range groups {
		out[k] = v
	}
	m.db.Set("goroku.security", "sgroups", out)
	m.applyGroupsToManager()
}

func (m *GorokuSecurity) getOwnerList() *goroku.PointerList {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return nil
	}
	dispatcher := loader.GetDispatcher()
	if dispatcher == nil {
		return nil
	}
	val := reflect.ValueOf(dispatcher).Elem()
	securityField := val.FieldByName("security")
	if !securityField.IsValid() {
		return nil
	}
	securityPtr := reflect.NewAt(securityField.Type(), unsafe.Pointer(securityField.UnsafeAddr())).Elem().Interface()

	smVal := reflect.ValueOf(securityPtr).Elem()
	ownerField := smVal.FieldByName("owner")
	if !ownerField.IsValid() {
		return nil
	}
	return reflect.NewAt(ownerField.Type(), unsafe.Pointer(ownerField.UnsafeAddr())).Elem().Interface().(*goroku.PointerList)
}

func (m *GorokuSecurity) getSecurityManager() *goroku.SecurityManager {
	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return nil
	}
	dispatcher := loader.GetDispatcher()
	if dispatcher == nil {
		return nil
	}
	return dispatcher.GetSecurityManager()
}

type resolvedSecurityUser struct {
	ID   int64
	Name string
	URL  string
}

func inputPeerUserID(peer tg.InputPeerClass, selfID int64) int64 {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return selfID
	case *tg.InputPeerUser:
		return p.UserID
	default:
		return 0
	}
}

func userClassFromFull(full interface{}) interface{} {
	switch f := full.(type) {
	case *tg.UsersUserFull:
		if len(f.Users) > 0 {
			return f.Users[0]
		}
	}
	return nil
}

func (m *GorokuSecurity) resolveSecurityUser(ref interface{}) (resolvedSecurityUser, bool) {
	var res resolvedSecurityUser
	if m.client == nil {
		return res, false
	}
	peer, err := m.client.ResolvePeer(ref)
	if err != nil {
		if id, ok := ref.(int64); ok && id > 0 {
			res.ID = id
			res.Name = fmt.Sprintf("User%d", id)
			res.URL = fmt.Sprintf("tg://user?id=%d", id)
			return res, true
		}
		return res, false
	}
	id := inputPeerUserID(peer, m.client.TGID)
	if id == 0 {
		return res, false
	}
	res.ID = id
	res.Name = fmt.Sprintf("User%d", id)
	res.URL = fmt.Sprintf("tg://user?id=%d", id)
	if full, err := m.client.GetFullUser(ref, 3600, false); err == nil {
		entity := userClassFromFull(full)
		if name := getDisplayName(entity); name != "" {
			res.Name = name
		}
		if url := utils.GetEntityURL(entity, false); url != "" {
			res.URL = url
		}
	}
	return res, true
}

func (m *GorokuSecurity) resolveUserFromMessage(msg *goroku.Message) (resolvedSecurityUser, bool) {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		arg := strings.TrimSpace(strings.TrimPrefix(parts[1], "@"))
		if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
			return m.resolveSecurityUser(id)
		}
		return m.resolveSecurityUser(arg)
	}
	if msg.ReplyToMsgID != 0 {
		if replyMsg, err := msg.GetReplyMessage(); err == nil && replyMsg != nil && replyMsg.SenderID != 0 {
			return m.resolveSecurityUser(replyMsg.SenderID)
		}
	}
	return resolvedSecurityUser{}, false
}

func pointerContainsID(pl *goroku.PointerList, id int64) bool {
	if pl == nil {
		return false
	}
	for _, item := range pl.ToSlice() {
		if interfaceToInt64(item) == id {
			return true
		}
	}
	return false
}

func pointerRemoveID(pl *goroku.PointerList, id int64) bool {
	if pl == nil {
		return false
	}
	slice := pl.ToSlice()
	for i, item := range slice {
		if interfaceToInt64(item) == id {
			pl.Remove(i)
			return true
		}
	}
	return false
}

func interfaceToInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		id, _ := x.Int64()
		return id
	}
	return 0
}

func parseUserID(client *goroku.CustomTelegramClient, arg string) (int64, string) {
	arg = strings.TrimSpace(strings.TrimPrefix(arg, "@"))
	if arg == "" {
		return 0, ""
	}
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return id, fmt.Sprintf("User%d", id)
	}
	if client != nil {
		peer, err := client.ResolvePeer(arg)
		if err == nil {
			id := inputPeerUserID(peer, client.TGID)
			if id != 0 {
				name := arg
				if full, err := client.GetFullUser(arg, 3600, false); err == nil {
					if display := getDisplayName(userClassFromFull(full)); display != "" {
						name = display
					}
				}
				return id, name
			}
		}
	}
	return 0, ""
}

func (m *GorokuSecurity) OwnerCmd(msg *goroku.Message) error {
	ol := m.getOwnerList()
	if ol == nil {
		return msg.Answer(m.getTrans("no_owner", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b>Нет пользователей в группе</b> <code>owner</code>"))
	}

	seen := map[int64]bool{}
	var users []resolvedSecurityUser
	for _, item := range ol.ToSlice() {
		id := interfaceToInt64(item)
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		if u, ok := m.resolveSecurityUser(id); ok {
			users = append(users, u)
		} else {
			users = append(users, resolvedSecurityUser{ID: id, Name: fmt.Sprintf("User%d", id), URL: fmt.Sprintf("tg://user?id=%d", id)})
		}
	}
	if m.client != nil && m.client.TGID != 0 && !seen[m.client.TGID] {
		if u, ok := m.resolveSecurityUser(m.client.TGID); ok {
			users = append(users, u)
		} else {
			users = append(users, resolvedSecurityUser{ID: m.client.TGID, Name: fmt.Sprintf("User%d", m.client.TGID), URL: fmt.Sprintf("tg://user?id=%d", m.client.TGID)})
		}
	}

	if len(users) == 0 {
		return msg.Answer(m.getTrans("no_owner", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b>Нет пользователей в группе</b> <code>owner</code>"))
	}

	prefixes := map[string]interface{}{}
	if raw, ok := m.db.Get("goroku.main", "command_prefixes", map[string]interface{}{}).(map[string]interface{}); ok {
		prefixes = raw
	}
	var lines []string
	for _, u := range users {
		line := fmt.Sprintf("<tg-emoji emoji-id=4974307891025543730>▫️</tg-emoji> <b><a href=\"%s\">%s</a></b>", u.URL, utils.EscapeHTML(u.Name))
		if p, ok := prefixes[strconv.FormatInt(u.ID, 10)]; ok && fmt.Sprintf("%v", p) != "" {
			line += fmt.Sprintf(" (%s)", utils.EscapeHTML(fmt.Sprintf("%v", p)))
		}
		lines = append(lines, line)
	}

	template := m.getTrans("owner_list", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b>Пользователи группы</b> <code>owner</code><b>:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

func (m *GorokuSecurity) AddownerCmd(msg *goroku.Message) error {
	user, ok := m.resolveUserFromMessage(msg)
	if !ok {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Укажи, кому выдавать права</b>"))
	}
	if user.ID == m.client.TGID {
		return msg.Answer(m.getTrans("self", "<tg-emoji emoji-id=5447644880824181073>⚠️</tg-emoji> <b>Нельзя управлять своими правами!</b>"))
	}

	ol := m.getOwnerList()
	if ol != nil && !pointerContainsID(ol, user.ID) {
		ol.Append(user.ID)
	}

	template := m.getTrans("owner_added", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b><a href=\"tg://user?id={0}\">{1}</a> добавлен в группу</b> <code>owner</code>")
	return msg.Answer(formatTrans(template, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name)))
}

func (m *GorokuSecurity) DelownerCmd(msg *goroku.Message) error {
	user, ok := m.resolveUserFromMessage(msg)
	if !ok {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Укажи, кому выдавать права</b>"))
	}
	if user.ID == m.client.TGID {
		return msg.Answer(m.getTrans("self", "<tg-emoji emoji-id=5447644880824181073>⚠️</tg-emoji> <b>Нельзя управлять своими правами!</b>"))
	}

	pointerRemoveID(m.getOwnerList(), user.ID)
	template := m.getTrans("owner_removed", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b><a href=\"tg://user?id={0}\">{1}</a> удален из группы</b> <code>owner</code>")
	return msg.Answer(formatTrans(template, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name)))
}

func (m *GorokuSecurity) SudoCmd(msg *goroku.Message) error {
	raw := m.db.Get("goroku.security", "sudo", []interface{}{})
	var lines []string
	if slice, ok := raw.([]interface{}); ok {
		for _, item := range slice {
			var id int64
			switch v := item.(type) {
			case float64:
				id = int64(v)
			case int64:
				id = v
			}
			if id != 0 {
				lines = append(lines, fmt.Sprintf("<tg-emoji emoji-id=4974307891025543730>▫️</tg-emoji> <b><a href=\"tg://user?id=%d\">User%d</a></b>", id, id))
			}
		}
	}

	if len(lines) == 0 {
		template := m.getTrans("no_owner", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b>Нет пользователей в группе</b> <code>owner</code>")
		return msg.Answer(strings.ReplaceAll(template, "owner", "sudo"))
	}

	template := m.getTrans("owner_list", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b>Пользователи группы</b> <code>owner</code><b>:</b>\n\n<blockquote expandable>{0}</blockquote>")
	template = strings.ReplaceAll(template, "owner", "sudo")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

func (m *GorokuSecurity) AddsudoCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	var targetUserID int64
	var targetUserName string

	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		targetUserID, targetUserName = parseUserID(m.client, parts[1])
	} else if msg.ReplyToMsgID != 0 {
		replyMsg, err := msg.GetReplyMessage()
		if err == nil && replyMsg != nil {
			targetUserID = replyMsg.SenderID
			targetUserName = fmt.Sprintf("User%d", targetUserID)
		}
	}

	if targetUserID == 0 {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи, кому выдавать права</b>"))
	}

	if targetUserID == m.client.TGID {
		return msg.Answer(m.getTrans("self", "<tg-emoji emoji-id=5447644880824181073>⚠️</emoji> <b>Нельзя управлять своими правами!</b>"))
	}

	raw := m.db.Get("goroku.security", "sudo", []interface{}{})
	var sudoList []interface{}
	alreadyPresent := false
	if slice, ok := raw.([]interface{}); ok {
		sudoList = slice
		for _, item := range slice {
			var sid int64
			switch v := item.(type) {
			case float64:
				sid = int64(v)
			case int64:
				sid = v
			}
			if sid == targetUserID {
				alreadyPresent = true
				break
			}
		}
	}
	if !alreadyPresent {
		sudoList = append(sudoList, targetUserID)
		m.db.Set("goroku.security", "sudo", sudoList)
	}

	template := m.getTrans("owner_added", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b><a href=\"tg://user?id={0}\">{1}</a> добавлен в группу</b> <code>owner</code>")
	template = strings.ReplaceAll(template, "owner", "sudo")
	return msg.Answer(formatTrans(template, strconv.FormatInt(targetUserID, 10), targetUserName))
}

func (m *GorokuSecurity) DelsudoCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	var targetUserID int64
	var targetUserName string

	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		targetUserID, targetUserName = parseUserID(m.client, parts[1])
	} else if msg.ReplyToMsgID != 0 {
		replyMsg, err := msg.GetReplyMessage()
		if err == nil && replyMsg != nil {
			targetUserID = replyMsg.SenderID
			targetUserName = fmt.Sprintf("User%d", targetUserID)
		}
	}

	if targetUserID == 0 {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи, кому выдавать права</b>"))
	}

	if targetUserID == m.client.TGID {
		return msg.Answer(m.getTrans("self", "<tg-emoji emoji-id=5447644880824181073>⚠️</emoji> <b>Нельзя управлять своими правами!</b>"))
	}

	raw := m.db.Get("goroku.security", "sudo", []interface{}{})
	var sudoList []interface{}
	foundIdx := -1
	if slice, ok := raw.([]interface{}); ok {
		for idx, item := range slice {
			var sid int64
			switch v := item.(type) {
			case float64:
				sid = int64(v)
			case int64:
				sid = v
			}
			if sid == targetUserID {
				foundIdx = idx
				break
			}
		}
		if foundIdx != -1 {
			sudoList = append(slice[:foundIdx], slice[foundIdx+1:]...)
			m.db.Set("goroku.security", "sudo", sudoList)
		}
	}

	template := m.getTrans("owner_removed", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b><a href=\"tg://user?id={0}\">{1}</a> удален из группы</b> <code>owner</code>")
	template = strings.ReplaceAll(template, "owner", "sudo")
	return msg.Answer(formatTrans(template, strconv.FormatInt(targetUserID, 10), targetUserName))
}

func (m *GorokuSecurity) SecurityCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}

	if args != "" {
		loader, ok := m.client.Loader.(*goroku.Modules)
		if !ok || loader == nil {
			return msg.Answer("❌ Error: Modules loader not ready.")
		}
		_, exists := loader.Dispatch(args)
		if !exists {
			template := m.getTrans("no_command", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Команда</b> <code>{0}</code> <b>не найдена!</b>")
			return msg.Answer(formatTrans(template, args))
		}

		template := m.getTrans("permissions", "🔐 <b>Здесь можно настроить разрешения для команды</b> <code>{0}{1}</code>")
		prefix := "."
		if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
			prefix = pVal
		}
		return msg.Answer(formatTrans(template, prefix, args))
	}

	return msg.Answer(m.getTrans("global", "🔐 <b>Здесь можно настроить глобальную исключающую маску. Если тумблер выключен здесь, он выключен для всех команд</b>"))
}

func (m *GorokuSecurity) TsecCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_target", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указана цель правила безопасности</b>"))
	}

	args := strings.Fields(parts[1])
	if len(args) < 2 {
		return msg.Answer(m.getTrans("no_rule", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указано правило безопасности (модуль или команда)</b>"))
	}

	targetType := strings.ToLower(args[0])
	if targetType != "user" && targetType != "chat" && targetType != "sgroup" {
		return msg.Answer(m.getTrans("what", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Вам нужно указать тип цели первым аргументом (</b><code>user</code> <b>or</b> <code>chat</code><b>)</b>"))
	}

	targetID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return msg.Answer(m.getTrans("not_a_user", "<tg-emoji emoji-id=5447644880824181073>⚠️</tg-emoji> <b>Указанная цель - не пользователь</b>"))
	}

	rule := args[2] // e.g. "command/eval" or "module/Eval"
	duration := 0
	if len(args) >= 4 {
		duration, _ = strconv.Atoi(args[3])
	}

	sm := m.getSecurityManager()
	if sm == nil {
		return msg.Answer("Security manager not available")
	}

	ruleParts := strings.SplitN(rule, "/", 2)
	ruleType := "command"
	ruleName := rule
	if len(ruleParts) == 2 {
		ruleType = ruleParts[0]
		ruleName = ruleParts[1]
	}

	sm.AddRule(targetType, targetID, ruleType, ruleName, duration)

	var forStr string
	if duration > 0 {
		forStr = m.getTrans("until", "до") + " " + time.Now().Add(time.Duration(duration)*time.Second).Format("2006-01-02 15:04:05")
	} else {
		forStr = m.getTrans("forever", "навсегда")
	}

	template := m.getTrans("rule_added", "🔐 <b>Вы выдали {0} <a href='{1}'>{2}</a> право использовать {3} <code>{4}</code> {5}</b>")
	typeStr := m.getTrans(targetType, targetType)
	ruleTypeStr := m.getTrans(ruleType, ruleType)
	return msg.Answer(formatTrans(template, typeStr, "", strconv.FormatInt(targetID, 10), ruleTypeStr, ruleName, forStr))
}

func (m *GorokuSecurity) TtsecCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_target", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указана цель правила безопасности</b>"))
	}

	args := strings.Fields(parts[1])
	if len(args) == 0 {
		return msg.Answer(m.getTrans("no_target", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указана цель правила безопасности</b>"))
	}

	targetType := strings.ToLower(args[0])
	if targetType != "user" && targetType != "chat" && targetType != "sgroup" {
		return msg.Answer(m.getTrans("what", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Вам нужно указать тип цели первым аргументом (</b><code>user</code> <b>or</b> <code>chat</code><b>)</b>"))
	}

	targetID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return msg.Answer(m.getTrans("not_a_user", "<tg-emoji emoji-id=5447644880824181073>⚠️</emoji> <b>Указанная цель - не пользователь</b>"))
	}

	sm := m.getSecurityManager()
	if sm == nil {
		return msg.Answer("Security manager not available")
	}

	if len(args) >= 3 {
		removed := sm.RemoveRule(targetType, targetID, args[2])
		if removed {
			template := m.getTrans("rule_removed", "<tg-emoji emoji-id=5472308992514464048>🔐</tg-emoji> <b>Удалено правило безопасности для <a href=\"{0}\">{1}</a> (</b><code>{2}</code><b>)</b>")
			return msg.Answer(formatTrans(template, "", strconv.FormatInt(targetID, 10), args[2]))
		}
		return msg.Answer(m.getTrans("no_rules", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нет таргетированных правил безопасности</b>"))
	}

	removed := sm.RemoveRules(targetType, targetID)
	if removed {
		template := m.getTrans("rules_removed", "<tg-emoji emoji-id=5472308992514464048>🔐</tg-emoji> <b>Правила таргетированной безопасности для <a href=\"{0}\">{1}</a> удалены</b>")
		return msg.Answer(formatTrans(template, "", strconv.FormatInt(targetID, 10)))
	}
	return msg.Answer(m.getTrans("no_rules", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нет таргетированных правил безопасности</b>"))
}

func (m *GorokuSecurity) NewsgroupCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	name := strings.TrimSpace(parts[1])
	groups := m.loadGroups()

	if _, exists := groups[name]; exists {
		template := m.getTrans("sgroup_already_exists", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Группа безопасности</b> <code>{0}</code> <b>уже существует</b>")
		return msg.Answer(formatTrans(template, name))
	}

	groups[name] = securityGroup{
		Users:       []int64{},
		Permissions: []map[string]interface{}{},
	}
	m.saveGroups(groups)

	template := m.getTrans("created_sgroup", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Создана группа безопасности</b> <code>{0}</code>")
	return msg.Answer(formatTrans(template, name))
}

func (m *GorokuSecurity) SgroupsCmd(msg *goroku.Message) error {
	groups := m.loadGroups()

	if len(groups) == 0 {
		return msg.Answer(m.getTrans("no_rules", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Нет таргетированных правил безопасности</b>"))
	}

	var lines []string
	liTemplate := m.getTrans("sgroup_li", "<tg-emoji emoji-id=4974264756668990388>▫️</tg-emoji> <code>{0}</code> · <b>{1} пользовател(-ей)</b> · <b>{2} правил(-о)</b>")
	for name, sg := range groups {
		lines = append(lines, formatTrans(liTemplate, name, strconv.Itoa(len(sg.Users)), strconv.Itoa(len(sg.Permissions))))
	}
	sort.Strings(lines)

	template := m.getTrans("sgroups_list", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Группы безопасности:</b>\n\n{0}")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

func (m *GorokuSecurity) SgroupaddCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	args := strings.Fields(parts[1])
	if len(args) < 1 {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	groupName := args[0]
	var targetUserID int64
	var targetUserName string

	if len(args) >= 2 {
		targetUserID, targetUserName = parseUserID(m.client, args[1])
	} else if msg.ReplyToMsgID != 0 {
		replyMsg, err := msg.GetReplyMessage()
		if err == nil && replyMsg != nil {
			targetUserID = replyMsg.SenderID
			targetUserName = fmt.Sprintf("User%d", targetUserID)
		}
	}

	if targetUserID == 0 {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи, кому выдавать права</b>"))
	}

	groups := m.loadGroups()
	sg, exists := groups[groupName]
	if !exists {
		template := m.getTrans("sgroup_not_found", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Группа безопасности</b> <code>{0}</code> <b>не найдена</b>")
		return msg.Answer(formatTrans(template, groupName))
	}

	for _, uid := range sg.Users {
		if uid == targetUserID {
			template := m.getTrans("user_already_in_sgroup", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Пользователь</b> <code>{0}</code> <b>уже состоит в группе безопасности</b> <code>{1}</code>")
			return msg.Answer(formatTrans(template, targetUserName, groupName))
		}
	}

	sg.Users = append(sg.Users, targetUserID)
	groups[groupName] = sg
	m.saveGroups(groups)

	template := m.getTrans("user_added_to_sgroup", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Пользователь</b> <code>{0}</code> <b>добавлен в группу безопасности</b> <code>{1}</code>")
	return msg.Answer(formatTrans(template, targetUserName, groupName))
}

func (m *GorokuSecurity) SgroupdelCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	args := strings.Fields(parts[1])
	if len(args) < 1 {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	groupName := args[0]
	var targetUserID int64
	var targetUserName string

	if len(args) >= 2 {
		targetUserID, targetUserName = parseUserID(m.client, args[1])
	} else if msg.ReplyToMsgID != 0 {
		replyMsg, err := msg.GetReplyMessage()
		if err == nil && replyMsg != nil {
			targetUserID = replyMsg.SenderID
			targetUserName = fmt.Sprintf("User%d", targetUserID)
		}
	}

	if targetUserID == 0 {
		return msg.Answer(m.getTrans("no_user", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Укажи, кому выдавать права</b>"))
	}

	groups := m.loadGroups()
	sg, exists := groups[groupName]
	if !exists {
		template := m.getTrans("sgroup_not_found", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Группа безопасности</b> <code>{0}</code> <b>не найдена</b>")
		return msg.Answer(formatTrans(template, groupName))
	}

	foundIdx := -1
	for i, uid := range sg.Users {
		if uid == targetUserID {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		template := m.getTrans("user_not_in_sgroup", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Пользователь</b> <code>{0}</code> <b>не состоит в группе безопасности</b> <code>{1}</code>")
		return msg.Answer(formatTrans(template, targetUserName, groupName))
	}

	sg.Users = append(sg.Users[:foundIdx], sg.Users[foundIdx+1:]...)
	groups[groupName] = sg
	m.saveGroups(groups)

	template := m.getTrans("user_removed_from_sgroup", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Пользователь</b> <code>{0}</code> <b>удален из группы</b> <code>{1}</code>")
	return msg.Answer(formatTrans(template, targetUserName, groupName))
}

func (m *GorokuSecurity) applyGroupsToManager() {
	sm := m.getSecurityManager()
	if sm == nil {
		return
	}
	groups := m.loadGroups()
	smGroups := make(map[string]goroku.SecurityGroup)
	for name, g := range groups {
		smGroups[name] = goroku.SecurityGroup{
			Name:        name,
			Users:       g.Users,
			Permissions: g.Permissions,
		}
	}
	sm.ApplySgroups(smGroups)
}

func (m *GorokuSecurity) SgroupCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	name := strings.TrimSpace(parts[1])
	groups := m.loadGroups()

	sg, exists := groups[name]
	if !exists {
		template := m.getTrans("sgroup_not_found", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Группа безопасности</b> <code>{0}</code> <b>не найдена</b>")
		return msg.Answer(formatTrans(template, name))
	}

	var usersList []string
	for _, uid := range sg.Users {
		usersList = append(usersList, fmt.Sprintf("<tg-emoji emoji-id=4974307891025543730>▫️</tg-emoji> <b><a href=\"tg://user?id=%d\">User%d</a></b>", uid, uid))
	}
	usersText := m.getTrans("no_users", "<tg-emoji emoji-id=5870772616305839506>👥</tg-emoji> <b>Нет пользователей</b>")
	if len(usersList) > 0 {
		template := m.getTrans("users_list", "<tg-emoji emoji-id=5870772616305839506>👥</tg-emoji> <b>Пользователи:</b>\n{0}\n")
		usersText = formatTrans(template, strings.Join(usersList, "\n"))
	}

	var permsList []string
	for _, p := range sg.Permissions {
		ruleType := fmt.Sprintf("%v", p["rule_type"])
		rule := fmt.Sprintf("%v", p["rule"])
		expiresVal := p["expires"]
		var expiresStr string
		if expiresVal != nil {
			var ts int64
			switch t := expiresVal.(type) {
			case float64:
				ts = int64(t)
			case int64:
				ts = t
			case int:
				ts = int64(t)
			}
			if ts > 0 {
				expiresStr = m.getTrans("until", "до") + " " + time.Unix(ts, 0).Format("2006-01-02 15:04:05")
			} else {
				expiresStr = m.getTrans("forever", "навсегда")
			}
		} else {
			expiresStr = m.getTrans("forever", "навсегда")
		}
		permsList = append(permsList, fmt.Sprintf("<tg-emoji emoji-id=4974307891025543730>▫️</tg-emoji> <b>%s</b> <code>%s</code> <b>%s</b>", ruleType, rule, expiresStr))
	}
	permsText := m.getTrans("no_permissions", "<tg-emoji emoji-id=5870450390679425417>🗒</tg-emoji> <b>Нет разрешений</b>")
	if len(permsList) > 0 {
		template := m.getTrans("permissions_list", "<tg-emoji emoji-id=5870450390679425417>🗒</tg-emoji> <b>Права доступа:</b>\n{0}\n")
		permsText = formatTrans(template, strings.Join(permsList, "\n"))
	}

	template := m.getTrans("sgroup_info", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Информация о группе безопасности</b> <code>{0}</code>:\n\n{1}\n{2}")
	msg.Text = formatTrans(template, name, usersText, permsText)
	return msg.Answer(msg.Text)
}

func (m *GorokuSecurity) DelsgroupCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return msg.Answer(m.getTrans("no_args", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указаны аргументы</b>"))
	}

	name := strings.TrimSpace(parts[1])
	groups := m.loadGroups()

	if _, exists := groups[name]; !exists {
		template := m.getTrans("sgroup_not_found", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Группа безопасности</b> <code>{0}</code> <b>не найдена</b>")
		return msg.Answer(formatTrans(template, name))
	}

	delete(groups, name)
	m.saveGroups(groups)

	template := m.getTrans("deleted_sgroup", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <b>Группа безопасности</b> <code>{0}</code> <b>удалена</b>")
	return msg.Answer(formatTrans(template, name))
}
