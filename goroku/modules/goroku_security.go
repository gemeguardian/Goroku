package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
		"ownerlist": m.OwnerCmd,
		"owneradd":  m.AddownerCmd,
		"ownerrm":   m.DelownerCmd,
		"sudolist":  m.SudoCmd,
		"sudoadd":   m.AddsudoCmd,
		"sudorm":    m.DelsudoCmd,
		"security":  m.SecurityCmd,
		"inlinesec": m.InlinesecCmd,
		"querysec":  m.QuerysecCmd,
		"tsec":      m.TsecCmd,
		"tsecrm":    m.TtsecCmd,
		"tsecclr":   m.TsecclrCmd,
		"newsgroup": m.NewsgroupCmd,
		"sgroups":   m.SgroupsCmd,
		"sgroup":    m.SgroupCmd,
		"delsgroup": m.DelsgroupCmd,
		"sgroupadd": m.SgroupaddCmd,
		"sgroupdel": m.SgroupdelCmd,
	}
}

func (m *GorokuSecurity) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"ownerlist": {Aliases: []string{"owner"}},
		"owneradd":  {Aliases: []string{"addowner"}},
		"ownerrm":   {Aliases: []string{"delowner"}},
		"sudolist":  {Aliases: []string{"sudo"}},
		"sudoadd":   {Aliases: []string{"addsudo"}},
		"sudorm":    {Aliases: []string{"delsudo"}},
		"tsecrm":    {Aliases: []string{"ttsec"}},
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

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		ol := m.getOwnerList()
		if ol != nil && !pointerContainsID(ol, user.ID) {
			ol.Append(user.ID)
		}
		template := m.getTrans("owner_added", "<tg-emoji emoji-id=5386399931378440814>😎</tg-emoji> <b><a href=\"tg://user?id={0}\">{1}</a> добавлен в группу</b> <code>owner</code>")
		return msg.Answer(formatTrans(template, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name)))
	}

	warningTemplate := m.getTrans("warning", "⚠️ <b>Ты действительно хочешь добавить <a href=\"tg://user?id={0}\">{1}</a> в группу <code>{2}</code>!</b>")
	warningText := formatTrans(warningTemplate, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name), "owner")

	markup := [][]inline.Button{
		{
			{
				Text: m.getTrans("cancel", "🚫 Отмена"),
				Handler: func(call inline.CallbackQuery) error {
					return closeForm(call)
				},
			},
			{
				Text: m.getTrans("confirm", "👑 Подтвердить"),
				Handler: func(call inline.CallbackQuery) error {
					ol := m.getOwnerList()
					if ol != nil && !pointerContainsID(ol, user.ID) {
						ol.Append(user.ID)
					}

					addedTemplate := m.getTrans("owner_added", "добавлен в группу owner")
					addedText := formatTrans(addedTemplate, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name))

					suggestTemplate := m.getTrans("suggest_nonick", "Хочешь ли ты включить NoNick для этого пользователя?")
					fullText := addedText + "\n\n" + suggestTemplate

					noNickMarkup := [][]inline.Button{
						{
							{
								Text: m.getTrans("cancel", "🚫 Отмена"),
								Handler: func(callSub inline.CallbackQuery) error {
									return closeForm(callSub)
								},
							},
							{
								Text: m.getTrans("enable_nonick_btn", "🔰 Включить"),
								Handler: func(callSub inline.CallbackQuery) error {
									rawUsers := m.db.Get("goroku.main", "nonickusers", []interface{}{})
									var list []interface{}
									if slice, ok := rawUsers.([]interface{}); ok {
										list = slice
									}
									alreadyIn := false
									for _, item := range list {
										if interfaceToInt64(item) == user.ID {
											alreadyIn = true
											break
										}
									}
									if !alreadyIn {
										list = append(list, user.ID)
										m.db.Set("goroku.main", "nonickusers", list)
									}

									nnTemplate := m.getTrans("user_nn", "NoNick для ... включен")
									nnText := formatTrans(nnTemplate, strconv.FormatInt(user.ID, 10), utils.EscapeHTML(user.Name))
									return callSub.Edit(nnText, tgbotapi.InlineKeyboardMarkup{})
								},
							},
						},
					}

					return call.Edit(fullText, im.GenerateMarkup(noNickMarkup))
				},
			},
		},
	}

	_, err := im.Form(warningText, msg, markup)
	return err
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

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		if args != "" {
			loader, ok := m.client.Loader.(*goroku.Modules)
			if !ok || loader == nil {
				return msg.Answer("❌ Error: Modules loader not ready.")
			}
			_, exists := loader.Dispatch(args)
			if !exists {
				template := m.getTrans("no_command", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Команда</b> <code>{0}</code> <b>не найдена!</b>")
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

	if args == "" {
		text := m.getTrans("global", "🔐 <b>Здесь можно настроить глобальную исключающую маску. Если тумблер выключен здесь, он выключен для всех команд</b>")
		markup := m.buildMarkupGlobal(false)
		_, err := im.Form(text, msg, markup)
		return err
	}

	loader, ok := m.client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
		return msg.Answer("❌ Error: Modules loader not ready.")
	}
	_, exists := loader.Dispatch(args)
	if !exists {
		template := m.getTrans("no_command", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Команда</b> <code>{0}</code> <b>не найдена!</b>")
		return msg.Answer(formatTrans(template, args))
	}

	prefix := "."
	if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefix = pVal
	}
	template := m.getTrans("permissions", "🔐 <b>Здесь можно настроить разрешения для команды</b> <code>{0}{1}</code>")
	textFormatted := formatTrans(template, prefix, args)

	markup := m.buildMarkupCommand(args, false)
	_, err := im.Form(textFormatted, msg, markup)
	return err
}

func (m *GorokuSecurity) TsecCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return m.listAllRules(msg)
	}

	args := strings.Fields(parts[1])
	if len(args) == 0 {
		return m.listAllRules(msg)
	}

	targetType := strings.ToLower(args[0])
	if targetType != "user" && targetType != "chat" && targetType != "sgroup" {
		return msg.Answer(m.getTrans("what", "Вам нужно указать тип цели первым аргументом (user, chat, sgroup)"))
	}

	var targetID int64
	var targetName string
	var targetURL string
	duration := extractTime(args)

	ruleArgsStart := 1

	if targetType == "sgroup" {
		if len(args) < 2 {
			return msg.Answer(m.getTrans("no_target", "Не указана цель правила безопасности"))
		}
		targetName = args[1]
		groups := m.loadGroups()
		if _, exists := groups[targetName]; !exists {
			template := m.getTrans("sgroup_not_found", "Группа безопасности {0} не найдена")
			return msg.Answer(formatTrans(template, targetName))
		}
		ruleArgsStart = 2
	} else if targetType == "user" {
		hasUserArg := false
		if len(args) >= 2 {
			arg := args[1]
			isTimeArg := false
			for _, suffix := range []string{"d", "h", "m", "s"} {
				if strings.HasSuffix(arg, suffix) {
					valStr := strings.TrimSuffix(arg, suffix)
					if _, err := strconv.Atoi(valStr); err == nil {
						isTimeArg = true
						break
					}
				}
			}
			if !isTimeArg {
				uid, uname := parseUserID(m.client, arg)
				if uid != 0 {
					targetID = uid
					targetName = uname
					targetURL = fmt.Sprintf("tg://user?id=%d", uid)
					if full, err := m.client.GetFullUser(uid, 3600, false); err == nil {
						if display := getDisplayName(userClassFromFull(full)); display != "" {
							targetName = display
						}
						if url := utils.GetEntityURL(userClassFromFull(full), false); url != "" {
							targetURL = url
						}
					}
					hasUserArg = true
					ruleArgsStart = 2
				}
			}
		}

		if !hasUserArg {
			if msg.ReplyToMsgID != 0 {
				replyMsg, err := msg.GetReplyMessage()
				if err == nil && replyMsg != nil {
					targetID = replyMsg.SenderID
				}
			} else if msg.IsPrivate {
				targetID = msg.ChatID
			}
			if targetID == 0 {
				return msg.Answer(m.getTrans("no_target", "Не указан пользователь"))
			}
			targetName = fmt.Sprintf("User%d", targetID)
			targetURL = fmt.Sprintf("tg://user?id=%d", targetID)
			if full, err := m.client.GetFullUser(targetID, 3600, false); err == nil {
				if display := getDisplayName(userClassFromFull(full)); display != "" {
					targetName = display
				}
				if url := utils.GetEntityURL(userClassFromFull(full), false); url != "" {
					targetURL = url
				}
			}
		}

		ol := m.getOwnerList()
		if pointerContainsID(ol, targetID) {
			return msg.Answer(m.getTrans("owner_target", "Этот пользователь - владелец..."))
		}
	} else if targetType == "chat" {
		hasChatArg := false
		if len(args) >= 2 {
			arg := args[1]
			isTimeArg := false
			for _, suffix := range []string{"d", "h", "m", "s"} {
				if strings.HasSuffix(arg, suffix) {
					valStr := strings.TrimSuffix(arg, suffix)
					if _, err := strconv.Atoi(valStr); err == nil {
						isTimeArg = true
						break
					}
				}
			}
			if !isTimeArg {
				if cid, err := strconv.ParseInt(arg, 10, 64); err == nil {
					targetID = cid
					hasChatArg = true
					ruleArgsStart = 2
				}
			}
		}
		if !hasChatArg {
			if msg.IsPrivate {
				return msg.Answer(m.getTrans("no_target", "Не указана цель правила безопасности"))
			}
			targetID = msg.ChatID
		}
		targetName = fmt.Sprintf("Chat%d", targetID)
		targetURL = ""
		if peer, err := m.client.ResolvePeer(targetID); err == nil {
			targetURL = utils.GetEntityURL(peer, false)
		}
	}

	if len(args) <= ruleArgsStart {
		return msg.Answer(m.getTrans("no_rule", "Не указано правило безопасности"))
	}

	var possibleRules []string
	for _, arg := range args[ruleArgsStart:] {
		isTimeArg := false
		for _, suffix := range []string{"d", "h", "m", "s"} {
			if strings.HasSuffix(arg, suffix) {
				valStr := strings.TrimSuffix(arg, suffix)
				if _, err := strconv.Atoi(valStr); err == nil {
					isTimeArg = true
					break
				}
			}
		}
		if isTimeArg {
			continue
		}
		possibleRules = append(possibleRules, m.lookupRules(arg)...)
	}

	if len(possibleRules) == 0 {
		return msg.Answer(m.getTrans("no_rule", "Не указано правило безопасности"))
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if len(possibleRules) > 1 && ok && im != nil && im.IsComplete() {
		var lines []string
		for _, rule := range possibleRules {
			ruleParts := strings.Split(rule, "/")
			line := fmt.Sprintf("🛡 <b>%s</b> <code>%s</code>", strings.Title(m.getTrans(ruleParts[0], ruleParts[0])), ruleParts[1])
			lines = append(lines, line)
		}
		textTemplate := m.getTrans("multiple_rules", "Не получилось однозначно распознать... Выберите то, которое имели ввиду:\n\n{0}")
		textFormatted := formatTrans(textTemplate, strings.Join(lines, "\n"))

		var buttons []inline.Button
		for _, r := range possibleRules {
			ruleParts := strings.Split(r, "/")
			ruleVal := r
			btnText := fmt.Sprintf("🛡 %s %s", strings.Title(m.getTrans(ruleParts[0], ruleParts[0])), ruleParts[1])
			buttons = append(buttons, inline.Button{
				Text: btnText,
				Handler: func(call inline.CallbackQuery) error {
					return m.showConfirmRuleForm(msg, targetType, targetID, targetName, targetURL, ruleVal, duration)
				},
			})
		}

		var markup [][]inline.Button
		for i := 0; i < len(buttons); i += 3 {
			end := i + 3
			if end > len(buttons) {
				end = len(buttons)
			}
			markup = append(markup, buttons[i:end])
		}

		_, err := im.Form(textFormatted, msg, markup)
		return err
	}

	return m.showConfirmRuleForm(msg, targetType, targetID, targetName, targetURL, possibleRules[0], duration)
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
		return msg.Answer(m.getTrans("what", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Вам нужно указать тип цели первым аргументом (user, chat, sgroup)</b>"))
	}

	if targetType == "sgroup" {
		if len(args) < 3 {
			return msg.Answer(m.getTrans("no_target", "Не указаны аргументы"))
		}
		groupName := args[1]
		ruleName := args[2]

		groups := m.loadGroups()
		group, exists := groups[groupName]
		if !exists {
			template := m.getTrans("sgroup_not_found", "Группа безопасности {0} не найдена")
			return msg.Answer(formatTrans(template, groupName))
		}

		any := false
		for i := len(group.Permissions) - 1; i >= 0; i-- {
			if group.Permissions[i]["rule"] == ruleName {
				group.Permissions = append(group.Permissions[:i], group.Permissions[i+1:]...)
				any = true
			}
		}

		if !any {
			return msg.Answer(m.getTrans("no_rules", "Нет правил безопасности"))
		}

		groups[groupName] = group
		m.saveGroups(groups)

		template := m.getTrans("rule_removed", "Удалено правило безопасности для...")
		return msg.Answer(formatTrans(template, "", utils.EscapeHTML(groupName), utils.EscapeHTML(ruleName)))
	}

	if len(args) < 2 {
		return msg.Answer(m.getTrans("no_target", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Не указана цель правила безопасности</b>"))
	}

	var targetID int64
	var targetName string
	var targetURL string

	if targetType == "user" {
		hasUserArg := false
		if len(args) >= 3 {
			uid, uname := parseUserID(m.client, args[1])
			if uid != 0 {
				targetID = uid
				targetName = uname
				targetURL = fmt.Sprintf("tg://user?id=%d", uid)
				hasUserArg = true
			}
		}
		if !hasUserArg {
			if msg.ReplyToMsgID != 0 {
				replyMsg, err := msg.GetReplyMessage()
				if err == nil && replyMsg != nil {
					targetID = replyMsg.SenderID
				}
			} else if msg.IsPrivate {
				targetID = msg.ChatID
			}
		}
		if targetID == 0 {
			return msg.Answer(m.getTrans("no_target", "Не указан пользователь"))
		}
		if targetName == "" {
			targetName = fmt.Sprintf("User%d", targetID)
			targetURL = fmt.Sprintf("tg://user?id=%d", targetID)
		}
	} else {
		hasChatArg := false
		if len(args) >= 3 {
			if cid, err := strconv.ParseInt(args[1], 10, 64); err == nil {
				targetID = cid
				hasChatArg = true
			}
		}
		if !hasChatArg {
			targetID = msg.ChatID
		}
		targetName = fmt.Sprintf("Chat%d", targetID)
	}

	sm := m.getSecurityManager()
	if sm == nil {
		return msg.Answer("Security manager not available")
	}

	ruleName := args[len(args)-1]
	removed := sm.RemoveRule(targetType, targetID, ruleName)
	if removed {
		template := m.getTrans("rule_removed", "Удалено правило безопасности...")
		return msg.Answer(formatTrans(template, targetURL, utils.EscapeHTML(targetName), utils.EscapeHTML(ruleName)))
	}

	return msg.Answer(m.getTrans("no_rules", "Нет таргетированных правил безопасности"))
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

func (m *GorokuSecurity) InlinesecCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	args = strings.ToLower(args)

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		return msg.Answer("❌ Error: Inline bot is not active.")
	}

	if args == "" {
		text := m.getTrans("global", "🔐 <b>Здесь можно настроить глобальную исключающую маску. Если тумблер выключен здесь, он выключен для всех команд</b>")
		markup := m.buildMarkupGlobal(true)
		_, err := im.Form(text, msg, markup)
		return err
	}

	exists := false
	vIm := reflect.ValueOf(im)
	mInlineModules := vIm.MethodByName("inlineModules")
	if mInlineModules.IsValid() {
		resMods := mInlineModules.Call(nil)
		if len(resMods) > 0 && resMods[0].Kind() == reflect.Slice {
			slice := resMods[0]
			for i := 0; i < slice.Len(); i++ {
				modItem := slice.Index(i).Interface()
				if inlineMod, ok := modItem.(inline.ModuleInlineHandlers); ok {
					for cmd := range inlineMod.InlineHandlers() {
						if strings.EqualFold(cmd, args) {
							exists = true
							break
						}
					}
				}
				if exists {
					break
				}
			}
		}
	}

	if !exists {
		template := m.getTrans("no_command", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Команда</b> <code>{0}</code> <b>не найдена!</b>")
		return msg.Answer(formatTrans(template, args))
	}

	prefix := "@" + im.BotUsername + " "
	textTemplate := m.getTrans("permissions", "🔐 <b>Здесь можно настроить разрешения для команды</b> <code>{0}{1}</code>")
	textFormatted := formatTrans(textTemplate, prefix, args)

	markup := m.buildMarkupCommand(args, true)
	_, err := im.Form(textFormatted, msg, markup)
	return err
}

func (m *GorokuSecurity) QuerysecCmd(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		return msg.Answer("❌ Error: Inline bot is not active.")
	}

	text := m.getTrans("querysec_info", "<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> Здесь вы можете переключить возможность использования инлайн запросов для всех сторонних пользователей")
	markup := m.buildMarkupQuerysec()
	_, err := im.Form(text, msg, markup)
	return err
}

func (m *GorokuSecurity) TsecclrCmd(msg *goroku.Message) error {
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
		return msg.Answer(m.getTrans("what", "<tg-emoji emoji-id=5210952531676504517>🚫</emoji> <b>Вам нужно указать тип цели первым аргументом (user, chat, sgroup)</b>"))
	}

	if targetType == "sgroup" {
		if len(args) < 2 {
			return msg.Answer(m.getTrans("no_target", "Не указана цель"))
		}
		groupName := args[1]
		groups := m.loadGroups()
		group, exists := groups[groupName]
		if !exists {
			template := m.getTrans("sgroup_not_found", "Группа безопасности {0} не найдена")
			return msg.Answer(formatTrans(template, groupName))
		}

		group.Permissions = []map[string]interface{}{}
		groups[groupName] = group
		m.saveGroups(groups)

		template := m.getTrans("rules_removed", "Правила безопасности для... удалены")
		return msg.Answer(formatTrans(template, "", utils.EscapeHTML(groupName)))
	}

	var targetID int64
	var targetName string
	var targetURL string

	if targetType == "user" {
		hasUserArg := false
		if len(args) >= 2 {
			uid, uname := parseUserID(m.client, args[1])
			if uid != 0 {
				targetID = uid
				targetName = uname
				targetURL = fmt.Sprintf("tg://user?id=%d", uid)
				hasUserArg = true
			}
		}
		if !hasUserArg {
			if msg.ReplyToMsgID != 0 {
				replyMsg, err := msg.GetReplyMessage()
				if err == nil && replyMsg != nil {
					targetID = replyMsg.SenderID
				}
			} else if msg.IsPrivate {
				targetID = msg.ChatID
			}
		}
		if targetID == 0 {
			return msg.Answer(m.getTrans("no_target", "Не указан пользователь"))
		}
		if targetName == "" {
			targetName = fmt.Sprintf("User%d", targetID)
			targetURL = fmt.Sprintf("tg://user?id=%d", targetID)
		}
	} else {
		hasChatArg := false
		if len(args) >= 2 {
			if cid, err := strconv.ParseInt(args[1], 10, 64); err == nil {
				targetID = cid
				hasChatArg = true
			}
		}
		if !hasChatArg {
			targetID = msg.ChatID
		}
		targetName = fmt.Sprintf("Chat%d", targetID)
	}

	sm := m.getSecurityManager()
	if sm == nil {
		return msg.Answer("Security manager not available")
	}

	removed := sm.RemoveRules(targetType, targetID)
	if removed {
		template := m.getTrans("rules_removed", "Правила безопасности для... удалены")
		return msg.Answer(formatTrans(template, targetURL, utils.EscapeHTML(targetName)))
	}

	return msg.Answer(m.getTrans("no_rules", "Нет таргетированных правил безопасности"))
}

func extractTime(args []string) int {
	units := []struct {
		suffix     string
		quantifier int
	}{
		{"d", 24 * 60 * 60},
		{"h", 60 * 60},
		{"m", 60},
		{"s", 1},
	}
	for _, unit := range units {
		for _, arg := range args {
			if strings.HasSuffix(arg, unit.suffix) {
				valStr := strings.TrimSuffix(arg, unit.suffix)
				if val, err := strconv.Atoi(valStr); err == nil {
					return val * unit.quantifier
				}
			}
		}
	}
	return 0
}

func (m *GorokuSecurity) convertTime(duration int) string {
	if duration <= 0 {
		return m.getTrans("forever", "навсегда")
	}
	if duration >= 24*60*60 {
		days := duration / (24 * 60 * 60)
		suffix := "day"
		if days > 1 {
			suffix = "days"
		}
		return fmt.Sprintf("%d %s", days, m.getTrans(suffix, "дня(-ей)"))
	}
	if duration >= 60*60 {
		hours := duration / (60 * 60)
		suffix := "hour"
		if hours > 1 {
			suffix = "hours"
		}
		return fmt.Sprintf("%d %s", hours, m.getTrans(suffix, "часа(-ов)"))
	}
	if duration >= 60 {
		minutes := duration / 60
		suffix := "minute"
		if minutes > 1 {
			suffix = "minutes"
		}
		return fmt.Sprintf("%d %s", minutes, m.getTrans(suffix, "минут(-ы)"))
	}
	suffix := "second"
	if duration > 1 {
		suffix = "seconds"
	}
	return fmt.Sprintf("%d %s", duration, m.getTrans(suffix, "секунд(-ы)"))
}

func (m *GorokuSecurity) convertTimeAbs(ts int64) string {
	if ts <= 0 {
		return m.getTrans("forever", "навсегда")
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func (m *GorokuSecurity) lookupRules(needle string) []string {
	var prefixes []string
	prefixes = append(prefixes, ".")
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefixes = append(prefixes, val)
	}

	command := needle
	for _, pref := range prefixes {
		if strings.HasPrefix(command, pref) {
			command = strings.TrimPrefix(command, pref)
		}
	}
	command = strings.ToLower(command)

	var results []string

	loader, ok := m.client.Loader.(*goroku.Modules)
	if ok && loader != nil {
		_, isCmd := loader.Dispatch(command)
		if isCmd {
			results = append(results, "command/"+command)
		}

		mod := loader.LookupByName(needle)
		if mod != nil {
			results = append(results, "module/"+mod.Name())
		}
	}

	if im, ok := m.client.GorokuInline.(*inline.InlineManager); ok && im != nil {
		vIm := reflect.ValueOf(im)
		mInlineModules := vIm.MethodByName("inlineModules")
		if mInlineModules.IsValid() {
			resMods := mInlineModules.Call(nil)
			if len(resMods) > 0 && resMods[0].Kind() == reflect.Slice {
				slice := resMods[0]
				for i := 0; i < slice.Len(); i++ {
					modItem := slice.Index(i).Interface()
					if inlineMod, ok := modItem.(inline.ModuleInlineHandlers); ok {
						for cmd := range inlineMod.InlineHandlers() {
							cleanNeedle := strings.TrimPrefix(strings.ToLower(needle), "@")
							if strings.EqualFold(cmd, cleanNeedle) {
								results = append(results, "inline/"+strings.ToLower(cmd))
							}
						}
					}
				}
			}
		}
	}

	return results
}

var securityGroups = []struct {
	Name string
	Bit  int
}{
	{"group_owner", goroku.GROUP_OWNER},
	{"group_admin_add_admins", goroku.GROUP_ADMIN_ADD_ADMINS},
	{"group_admin_change_info", goroku.GROUP_ADMIN_CHANGE_INFO},
	{"group_admin_ban_users", goroku.GROUP_ADMIN_BAN_USERS},
	{"group_admin_delete_messages", goroku.GROUP_ADMIN_DEL_MSGS},
	{"group_admin_pin_messages", goroku.GROUP_ADMIN_PIN_MSGS},
	{"group_admin_invite_users", goroku.GROUP_ADMIN_INVITE},
	{"group_admin", goroku.GROUP_ADMIN},
	{"group_member", goroku.GROUP_MEMBER},
	{"pm", goroku.PM},
	{"everyone", goroku.EVERYONE},
}

func (m *GorokuSecurity) getCommandMask(commandName string) int {
	sm := m.getSecurityManager()
	if sm == nil {
		return goroku.OWNER
	}
	key := ""
	loader, ok := m.client.Loader.(*goroku.Modules)
	if ok && loader != nil {
		for _, mod := range loader.GetModules() {
			if _, exists := mod.Commands()[commandName]; exists {
				key = fmt.Sprintf("%s.%s", mod.Name(), commandName)
				break
			}
		}
	}
	if key == "" {
		key = commandName
	}

	masksRaw := m.db.Get("goroku.security", "masks", map[string]interface{}{})
	if masks, ok := masksRaw.(map[string]interface{}); ok {
		for _, lookup := range []string{key, strings.ToLower(key)} {
			if val, exists := masks[lookup]; exists {
				return intFromInterface(val, goroku.OWNER)
			}
		}
	}

	if loader != nil {
		for _, mod := range loader.GetModules() {
			if _, exists := mod.Commands()[commandName]; exists {
				if secMod, ok := mod.(goroku.SecuredModule); ok {
					if mask, ok := secMod.CommandPermissions()[commandName]; ok {
						return mask
					}
				}
				break
			}
		}
	}

	return goroku.OWNER
}

func (m *GorokuSecurity) getBoundingMask() int {
	return intFromInterface(m.db.Get("goroku.security", "bounding_mask", goroku.DEFAULT_PERMISSIONS), goroku.DEFAULT_PERMISSIONS)
}

func (m *GorokuSecurity) buildMarkupGlobal(isInline bool) [][]inline.Button {
	mask := m.getBoundingMask()

	var buttons []inline.Button
	for _, sg := range securityGroups {
		if isInline && sg.Name != "everyone" {
			continue
		}
		bit := sg.Bit
		name := sg.Name
		hasBit := (mask & bit) != 0

		text := "🚫 " + m.getTrans(name, name)
		if hasBit {
			text = "✅ " + m.getTrans(name, name)
		}

		buttons = append(buttons, inline.Button{
			Text: text,
			Handler: func(call inline.CallbackQuery) error {
				newMask := m.getBoundingMask()
				if hasBit {
					newMask &= ^bit
				} else {
					newMask |= bit
				}
				m.db.Set("goroku.security", "bounding_mask", newMask)
				_ = call.Answer("Bounding mask value set!", false)

				im := m.client.GorokuInline.(*inline.InlineManager)
				newMarkup := im.GenerateMarkup(m.buildMarkupGlobal(isInline))
				return call.Edit(m.getTrans("global", "Global bounding mask..."), newMarkup)
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

	closeBtn := inline.Button{
		Text: m.getTrans("close_menu", "🙈 Закрыть это меню"),
		Handler: func(call inline.CallbackQuery) error {
			return closeForm(call)
		},
	}
	markup = append(markup, []inline.Button{closeBtn})

	return markup
}

func (m *GorokuSecurity) buildMarkupCommand(commandName string, isInline bool) [][]inline.Button {
	mask := m.getCommandMask(commandName)

	var buttons []inline.Button
	for _, sg := range securityGroups {
		if isInline && sg.Name != "everyone" {
			continue
		}
		bit := sg.Bit
		name := sg.Name
		hasBit := (mask & bit) != 0

		text := "🚫 " + m.getTrans(name, name)
		if hasBit {
			text = "✅ " + m.getTrans(name, name)
		}

		buttons = append(buttons, inline.Button{
			Text: text,
			Handler: func(call inline.CallbackQuery) error {
				newMask := m.getCommandMask(commandName)
				if hasBit {
					newMask &= ^bit
				} else {
					newMask |= bit
				}

				key := ""
				loader, ok := m.client.Loader.(*goroku.Modules)
				if ok && loader != nil {
					for _, mod := range loader.GetModules() {
						if _, exists := mod.Commands()[commandName]; exists {
							key = fmt.Sprintf("%s.%s", mod.Name(), commandName)
							break
						}
					}
				}
				if key == "" {
					key = commandName
				}

				masksRaw := m.db.Get("goroku.security", "masks", map[string]interface{}{})
				masks, _ := masksRaw.(map[string]interface{})
				if masks == nil {
					masks = make(map[string]interface{})
				}
				masks[key] = newMask
				masks[strings.ToLower(key)] = newMask
				m.db.Set("goroku.security", "masks", masks)

				bounding := m.getBoundingMask()
				if (bounding&bit) == 0 && !hasBit {
					alertText := "Security value set but not applied. Consider enabling this value in security global config."
					_ = call.Answer(alertText, true)
				} else {
					_ = call.Answer("Security value set!", false)
				}

				prefix := "."
				if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
					prefix = pVal
				}
				if isInline {
					prefix = "@" + call.Manager.BotUsername + " "
				}
				textTemplate := m.getTrans("permissions", "🔐 <b>Здесь можно настроить разрешения для команды</b> <code>{0}{1}</code>")
				textFormatted := formatTrans(textTemplate, prefix, commandName)

				im := m.client.GorokuInline.(*inline.InlineManager)
				newMarkup := im.GenerateMarkup(m.buildMarkupCommand(commandName, isInline))
				return call.Edit(textFormatted, newMarkup)
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

	closeBtn := inline.Button{
		Text: m.getTrans("close_menu", "🙈 Закрыть это меню"),
		Handler: func(call inline.CallbackQuery) error {
			return closeForm(call)
		},
	}
	markup = append(markup, []inline.Button{closeBtn})

	return markup
}

func (m *GorokuSecurity) buildMarkupQuerysec() [][]inline.Button {
	allowQuery := false
	raw := m.db.Get("goroku.security", "allow_inline_query", false)
	if val, ok := raw.(bool); ok {
		allowQuery = val
	}

	btnText := "❌"
	if allowQuery {
		btnText = "✅"
	}

	markup := [][]inline.Button{
		{
			{
				Text: btnText,
				Handler: func(call inline.CallbackQuery) error {
					newVal := !allowQuery
					m.db.Set("goroku.security", "allow_inline_query", newVal)
					_ = call.Answer("Inline query permission set!", false)

					im := m.client.GorokuInline.(*inline.InlineManager)
					newMarkup := im.GenerateMarkup(m.buildMarkupQuerysec())
					return call.Edit(m.getTrans("querysec_info", "Здесь вы можете переключить..."), newMarkup)
				},
			},
		},
		{
			{
				Text: m.getTrans("close_menu", "🙈 Закрыть это меню"),
				Handler: func(call inline.CallbackQuery) error {
					return closeForm(call)
				},
			},
		},
	}
	return markup
}

func (m *GorokuSecurity) listAllRules(msg *goroku.Message) error {
	sm := m.getSecurityManager()
	if sm == nil {
		return msg.Answer("❌ Security manager not ready")
	}

	var lines []string

	for _, rule := range sm.GetChatRules() {
		timeDiff := int(rule.Expires - time.Now().Unix())
		if rule.Expires <= 0 {
			timeDiff = 0
		}
		timeStr := m.convertTime(timeDiff)
		line := fmt.Sprintf("<tg-emoji emoji-id=6037355667365300960>👥</tg-emoji> <b><a href='%s'>%s</a> %s %s %s</b> <code>%s</code>",
			rule.EntityURL, utils.EscapeHTML(rule.EntityName), timeStr, m.getTrans("for", "на"), m.getTrans(rule.RuleType, rule.RuleType), rule.Rule)
		lines = append(lines, line)
	}

	for _, rule := range sm.GetUserRules() {
		timeDiff := int(rule.Expires - time.Now().Unix())
		if rule.Expires <= 0 {
			timeDiff = 0
		}
		timeStr := m.convertTime(timeDiff)
		line := fmt.Sprintf("<tg-emoji emoji-id=6037122016849432064>👤</tg-emoji> <b><a href='%s'>%s</a> %s %s %s</b> <code>%s</code>",
			rule.EntityURL, utils.EscapeHTML(rule.EntityName), timeStr, m.getTrans("for", "на"), m.getTrans(rule.RuleType, rule.RuleType), rule.Rule)
		lines = append(lines, line)
	}

	groups := m.loadGroups()
	for name, sg := range groups {
		for _, perm := range sg.Permissions {
			ruleType, _ := perm["rule_type"].(string)
			ruleName, _ := perm["rule"].(string)
			expiresVal := perm["expires"]
			var expires int64
			if expiresVal != nil {
				switch t := expiresVal.(type) {
				case float64:
					expires = int64(t)
				case int64:
					expires = t
				}
			}
			timeDiff := int(expires - time.Now().Unix())
			if expires <= 0 {
				timeDiff = 0
			}
			timeStr := m.convertTime(timeDiff)
			line := fmt.Sprintf("<tg-emoji emoji-id=5870704313440932932>🔒</tg-emoji> <code>%s</code> <b>%s %s %s</b> <code>%s</code>",
				utils.EscapeHTML(name), timeStr, m.getTrans("for", "на"), m.getTrans(ruleType, ruleType), ruleName)
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return msg.Answer(m.getTrans("no_rules", "Нет таргетированных правил безопасности"))
	}

	template := m.getTrans("rules", "<tg-emoji emoji-id=5472308992514464048>🔐</tg-emoji> <b>Таргетированные правила безопасности:</b>\n\n<blockquote expandable>{0}</blockquote>")
	return msg.Answer(formatTrans(template, strings.Join(lines, "\n")))
}

func (m *GorokuSecurity) showConfirmRuleForm(msg *goroku.Message, targetType string, targetID int64, targetName string, targetURL string, rule string, duration int) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		if targetType == "sgroup" {
			groups := m.loadGroups()
			sg, ok := groups[targetName]
			if ok {
				var expires int64
				if duration > 0 {
					expires = time.Now().Unix() + int64(duration)
				}
				sg.Permissions = append(sg.Permissions, map[string]interface{}{
					"target":      targetName,
					"rule_type":   strings.Split(rule, "/")[0],
					"rule":        strings.Split(rule, "/")[1],
					"expires":     expires,
					"entity_name": targetName,
					"entity_url":  "",
				})
				groups[targetName] = sg
				m.saveGroups(groups)
			}
		} else {
			sm := m.getSecurityManager()
			if sm != nil {
				sm.AddRule(targetType, targetID, strings.Split(rule, "/")[0], strings.Split(rule, "/")[1], duration)
			}
		}
		template := m.getTrans("rule_added", "Вы выдали право...")
		ruleParts := strings.Split(rule, "/")
		forStr := m.getTrans("forever", "навсегда")
		if duration > 0 {
			forStr = m.getTrans("until", "до") + " " + time.Now().Add(time.Duration(duration)*time.Second).Format("2006-01-02 15:04:05")
		}
		return msg.Answer(formatTrans(template, m.getTrans(targetType, targetType), targetURL, targetName, m.getTrans(ruleParts[0], ruleParts[0]), ruleParts[1], forStr))
	}

	ruleParts := strings.Split(rule, "/")
	forStr := m.getTrans("forever", "навсегда")
	if duration > 0 {
		forStr = m.getTrans("for", "на") + " " + m.convertTime(duration)
	}

	confirmTemplate := m.getTrans("confirm_rule", "Пожалуйста, подтвердите что хотите выдать...")
	confirmText := formatTrans(confirmTemplate, m.getTrans(targetType, targetType), targetURL, utils.EscapeHTML(targetName), m.getTrans(ruleParts[0], ruleParts[0]), ruleParts[1], forStr)

	markup := [][]inline.Button{
		{
			{
				Text: m.getTrans("cancel", "🚫 Отмена"),
				Handler: func(call inline.CallbackQuery) error {
					return closeForm(call)
				},
			},
			{
				Text: m.getTrans("confirm", "👑 Подтвердить"),
				Handler: func(call inline.CallbackQuery) error {
					if targetType == "sgroup" {
						groups := m.loadGroups()
						sg, ok := groups[targetName]
						if ok {
							var expires int64
							if duration > 0 {
								expires = time.Now().Unix() + int64(duration)
							}
							sg.Permissions = append(sg.Permissions, map[string]interface{}{
								"target":      targetName,
								"rule_type":   ruleParts[0],
								"rule":        ruleParts[1],
								"expires":     expires,
								"entity_name": targetName,
								"entity_url":  "",
							})
							groups[targetName] = sg
							m.saveGroups(groups)
						}
					} else {
						sm := m.getSecurityManager()
						if sm != nil {
							entityName := strconv.FormatInt(targetID, 10)
							entityURL := ""
							if full, err := m.client.GetFullUser(targetID, 3600, false); err == nil {
								entity := userClassFromFull(full)
								if display := getDisplayName(entity); display != "" {
									entityName = display
								}
								if url := utils.GetEntityURL(userClassFromFull(full), false); url != "" {
									entityURL = url
								}
							}
							var expires int64
							if duration > 0 {
								expires = time.Now().Unix() + int64(duration)
							}
							newRule := goroku.SecurityRule{
								Target:     targetID,
								RuleType:   ruleParts[0],
								Rule:       ruleParts[1],
								Expires:    expires,
								EntityName: entityName,
								EntityURL:  entityURL,
							}
							if targetType == "user" || targetType == "chat" {
								sm.AddSecurityRule(targetType, newRule)
							}
						}
					}

					template := m.getTrans("rule_added", "Вы выдали право...")
					ruleText := ruleParts[1]
					if ruleParts[0] == "inline" {
						ruleText = "@" + call.Manager.BotUsername + " " + ruleText
					}
					addedText := formatTrans(template, m.getTrans(targetType, targetType), targetURL, utils.EscapeHTML(targetName), m.getTrans(ruleParts[0], ruleParts[0]), ruleText, forStr)
					return call.Edit(addedText, tgbotapi.InlineKeyboardMarkup{})
				},
			},
		},
	}

	_, err := im.Form(confirmText, msg, markup)
	return err
}

func intFromInterface(v interface{}, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if parsed, err := strconv.Atoi(x); err == nil {
			return parsed
		}
	}
	return fallback
}
