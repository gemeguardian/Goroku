package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"goroku/goroku/web"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gotd/td/tg"
)

type GorokuWeb struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *GorokuWeb) Name() string {
	return "GorokuWeb"
}

func (m *GorokuWeb) Strings() map[string]string {
	return map[string]string{
		"name": "Goroku Web Module",
	}
}

func (m *GorokuWeb) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *GorokuWeb) ClientReady() error { return nil }
func (m *GorokuWeb) OnUnload() error    { return nil }
func (m *GorokuWeb) OnDlmod() error     { return nil }

func (m *GorokuWeb) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"webrestart":  m.WebrestartCmd,
		"webpanel":    m.WebpanelCmd,
		"webstop":     m.WebstopCmd,
		"approve_web": m.ApproveWebCmd,
		"addacc":      m.AddaccCmd,
	}
}

func (m *GorokuWeb) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"webpanel": {
			Aliases: []string{"weburl"},
		},
	}
}

func (m *GorokuWeb) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuWeb) WebrestartCmd(msg *goroku.Message) error {
	if web.Instance == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	port := 8080
	val := reflect.ValueOf(web.Instance).Elem()
	portVal := val.FieldByName("port")
	if portVal.IsValid() {
		port = int(portVal.Int())
	}

	web.Instance.Stop()
	go web.Instance.Start(port, true)

	msg.Text = "🔄 <b>Web server restarted.</b>"
	_ = msg.Answer(msg.Text)
	return nil
}

func (m *GorokuWeb) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *GorokuWeb) WebpanelCmd(msg *goroku.Message) error {
	if web.Instance == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	if os.Getenv("JAMHOST") != "" {
		template := m.getTrans("host_denied", "<tg-emoji emoji-id=6037254263187443802>💬</tg-emoji> Session addition commands are not available on your hosting, please contact your hosting administration.")
		return msg.Answer(template)
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	hasInline := ok && im != nil && im.IsComplete()

	if os.Getenv("LAVHOST") != "" {
		lavhostWeb := m.getTrans("lavhost_web", "🕸 <b>Web interface is available.</b>\n\n<i>To add account use button below.</i>")
		webBtnText := m.getTrans("web_btn", "🔗 Web Panel")
		url := web.Instance.GetURL(false)

		if hasInline {
			btn := inline.Button{
				Text: webBtnText,
				URL:  url,
			}
			_, err := im.Form(
				lavhostWeb,
				msg,
				[][]inline.Button{{btn}},
				inline.WithPhoto("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/web_interface.png"),
			)
			return err
		} else {
			return msg.Answer(fmt.Sprintf("%s\n\n<a href=\"%s\">%s</a>", lavhostWeb, url, webBtnText))
		}
	}

	force := strings.Contains(strings.ToLower(msg.Text), "force_insecure")

	if !force && !msg.IsPrivate {
		privacyLeakNowarn := m.getTrans("privacy_leak_nowarn", "⚠️ <b>WARNING! Sending link to the public chat will compromise your session!</b>\n\nYour user ID is <code>{}</code>. If you are sure you want to get the link here, press button below.")
		privacyLeak := m.getTrans("privacy_leak", "⚠️ <b>WARNING! Sending link to the public chat will compromise your session!</b>\n\nYour user ID is <code>{}</code>. If you are sure you want to get the link here, send <code>{}weburl force_insecure</code>.")

		prefix := "."
		if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
			prefix = val
		}

		if hasInline {
			text := formatTrans(privacyLeakNowarn, fmt.Sprintf("%d", m.client.TGID))
			btnYes := inline.Button{
				Text: m.getTrans("btn_yes", "🚸 Confirm anyway"),
				Handler: func(call inline.CallbackQuery) error {
					return m.showWebpanelTunnel(call, true)
				},
			}
			btnNo := inline.Button{
				Text:    m.getTrans("btn_no", "🔻 Close"),
				Handler: closeForm,
			}
			_, err := im.Form(
				text,
				msg,
				[][]inline.Button{{btnYes, btnNo}},
				inline.WithPhoto("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/web_interface.png"),
			)
			return err
		} else {
			text := formatTrans(privacyLeak, fmt.Sprintf("%d", m.client.TGID), prefix)
			return msg.Answer(text)
		}
	}

	return m.showWebpanelTunnel(msg, false)
}

func (m *GorokuWeb) showWebpanelTunnel(call interface{}, isCallback bool) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	hasInline := ok && im != nil && im.IsComplete()

	openingText := m.getTrans("opening_tunnel", "🕔 <b>Opening tunnel...</b>")
	waitBtn := inline.Button{
		Text: "🕔 Wait...",
		Data: "empty",
		Handler: func(c inline.CallbackQuery) error {
			return c.Answer("Please wait, the tunnel is opening...", false)
		},
	}
	waitMarkup := [][]inline.Button{{waitBtn}}

	if !hasInline {
		_ = call.(*goroku.Message).Answer(openingText)
		url := web.Instance.GetURL(true)
		openedText := m.getTrans("tunnel_opened", "✅ <b>Tunnel opened successfully!</b>")
		webBtnText := m.getTrans("web_btn", "🔗 Web Panel")
		return call.(*goroku.Message).Answer(fmt.Sprintf("%s\n\n<a href=\"%s\">%s</a>", openedText, url, webBtnText))
	}

	var inlineMsg *inline.InlineMessage
	var callbackQuery inline.CallbackQuery
	var err error

	if isCallback {
		callbackQuery = call.(inline.CallbackQuery)
		err = callbackQuery.Edit(openingText, im.GenerateMarkup(waitMarkup))
	} else {
		msg := call.(*goroku.Message)
		inlineMsg, err = im.Form(
			openingText,
			msg,
			waitMarkup,
			inline.WithPhoto("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/opening_tunnel.png"),
		)
	}
	if err != nil {
		return err
	}

	go func() {
		url := web.Instance.GetURL(true)
		openedText := m.getTrans("tunnel_opened", "✅ <b>Tunnel opened successfully!</b>")
		webBtnText := m.getTrans("web_btn", "🔗 Web Panel")
		linkBtn := inline.Button{
			Text: webBtnText,
			URL:  url,
		}
		linkMarkup := [][]inline.Button{{linkBtn}}

		if isCallback {
			_ = callbackQuery.Edit(openedText, im.GenerateMarkup(linkMarkup))
		} else if inlineMsg != nil {
			_ = inlineMsg.Edit(openedText, im.GenerateMarkup(linkMarkup))
		}
	}()

	return nil
}

func (m *GorokuWeb) WebstopCmd(msg *goroku.Message) error {
	if web.Instance == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	web.Instance.Stop()
	msg.Text = "🛑 <b>Web server stopped.</b>"
	_ = msg.Answer(msg.Text)
	return nil
}

func (m *GorokuWeb) ApproveWebCmd(msg *goroku.Message) error {
	if web.Instance == nil {
		return msg.Answer("❌ <b>Web server is not running or disabled.</b>")
	}

	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		return msg.Answer("❌ <b>Please specify the authorization token:</b> <code>.approve_web &lt;token&gt;</code>")
	}

	token := parts[1]
	if web.Instance.ApproveWebAuth(token) {
		return msg.Answer("✅ <b>Web Dashboard Authorization Approved!</b>")
	}

	return msg.Answer("❌ <b>Invalid or expired token.</b>")
}

func (m *GorokuWeb) AddaccCmd(msg *goroku.Message) error {
	if os.Getenv("JAMHOST") != "" || os.Getenv("LAVHOST") != "" {
		template := getTrans(m.translator, m.Name(), "host_denied", "<tg-emoji emoji-id=6037254263187443802>💬</tg-emoji> Session addition commands are not available on your hosting, please contact your hosting administration.")
		_ = msg.Answer(template)
		return nil
	}

	var targetUser *tg.User
	var targetID int64

	args := utils.GetArgs(msg.Text)
	if len(args) > 0 {
		entity, err := m.client.GetEntity(args[0], 0, false)
		if err == nil {
			if u, ok := entity.(*tg.User); ok {
				targetUser = u
				targetID = u.ID
			}
		}
	} else {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil {
			targetID = reply.SenderID
			entity, err := m.client.GetEntity(targetID, 0, false)
			if err == nil {
				if u, ok := entity.(*tg.User); ok {
					targetUser = u
				}
			}
		}
	}

	if targetUser == nil || targetUser.Bot {
		template := getTrans(m.translator, m.Name(), "invalid_target", "Reply to the message of the person you want to add, or specify their correct @username/id.")
		_ = msg.Answer(template)
		return nil
	}

	if targetID == m.client.TGID {
		template := getTrans(m.translator, m.Name(), "cant_add_self", "You can't add yourself.")
		_ = msg.Answer(template)
		return nil
	}

	forceInsecure := strings.Contains(strings.ToLower(msg.Text), "force_insecure")
	if forceInsecure {
		return m.InlineLogin(msg, targetUser, false)
	}

	prefixVal := m.db.Get("goroku.main", "command_prefix", ".")
	prefix := "."
	if p, ok := prefixVal.(string); ok {
		prefix = p
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil || !im.IsComplete() {
		template := getTrans(m.translator, m.Name(), "add_user_insecure", "Do you really want to add an account {} ({})? Use the <code>{}addacc {} force_insecure</code> command to confirm.")
		text := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID), prefix, fmt.Sprintf("%d", targetID))
		_ = msg.Answer(text)
		return nil
	}

	// Send confirmation form
	template := getTrans(m.translator, m.Name(), "add_user_confirm", "Do you really want to add an account {} ({})?")
	text := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID))

	btnYes := inline.Button{
		Text: getTrans(m.translator, m.Name(), "btn_yes", "🚸 Confirm anyway"),
		Handler: func(call inline.CallbackQuery) error {
			return m.InlineLoginCallback(call, targetUser)
		},
	}
	btnNo := inline.Button{
		Text:    getTrans(m.translator, m.Name(), "btn_no", "🔻 Close"),
		Handler: closeForm,
	}

	markup := [][]inline.Button{{btnYes, btnNo}}
	_, err := im.Form(text, msg, markup)
	if err != nil {
		template := getTrans(m.translator, m.Name(), "add_user_insecure", "Do you really want to add an account {} ({})? Use the <code>{}addacc {} force_insecure</code> command to confirm.")
		fallbackText := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID), prefix, fmt.Sprintf("%d", targetID))
		_ = msg.Answer(fallbackText)
	}

	return nil
}

func (m *GorokuWeb) InlineLoginCallback(call inline.CallbackQuery, targetUser *tg.User) error {
	return m.InlineLogin(call, targetUser, false)
}

func (m *GorokuWeb) InlineLogin(call interface{}, targetUser *tg.User, afterFail bool) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.translator, m.Name(), "enter_number_format", "Enter your phone number in the international format (for example, +79212345678):")
	if afterFail {
		incorrect := getTrans(m.translator, m.Name(), "incorrect_number", "You entered an incorrect phone number.\n\n")
		text = incorrect + text
	}

	btn := inline.Button{
		Text:  getTrans(m.translator, m.Name(), "enter_number", "Enter the number"),
		Input: getTrans(m.translator, m.Name(), "your_phone_number", "Your phone number"),
		InputHandler: func(c inline.CallbackQuery, data string) error {
			return m.InlinePhoneHandler(c, data, targetUser)
		},
	}

	markup := [][]inline.Button{{btn}}

	var err error
	if msg, ok := call.(*goroku.Message); ok {
		_, err = im.Form(text, msg, markup, inline.WithAlwaysAllow([]int64{targetUser.ID}))
	} else if c, ok := call.(inline.CallbackQuery); ok {
		err = c.Edit(text, im.GenerateMarkup(markup))
	}
	return err
}

func parsePhone(s string) string {
	var sb strings.Builder
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") {
		sb.WriteByte('+')
		s = s[1:]
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (m *GorokuWeb) InlinePhoneHandler(c inline.CallbackQuery, data string, targetUser *tg.User) error {
	phone := parsePhone(data)
	if phone == "" {
		return m.InlineLogin(c, targetUser, true)
	}

	tempClient := goroku.NewCustomTelegramClient(0)
	tempClient.APIID = m.client.APIID
	tempClient.APIHash = m.client.APIHash

	err := tempClient.Connect()
	if err != nil {
		return c.Edit(fmt.Sprintf("❌ Connection failed: %v", err), tgbotapi.InlineKeyboardMarkup{})
	}

	err = tempClient.SendCodeRequest(phone)
	if err != nil {
		tempClient.Disconnect()
		errMsg := err.Error()
		if strings.Contains(strings.ToLower(errMsg), "flood") {
			floodTemplate := getTrans(m.translator, m.Name(), "floodwait_error", "Too many attempts. Try again in {} seconds.")
			errMsg = strings.Replace(floodTemplate, "{}", "many", 1)
		}
		btnNo := inline.Button{
			Text:    getTrans(m.translator, m.Name(), "btn_no", "🔻 Close"),
			Handler: closeForm,
		}
		return c.Edit(fmt.Sprintf("❌ %s", errMsg), c.Manager.GenerateMarkup([][]inline.Button{{btnNo}}))
	}

	return m.PromptCode(c, tempClient, phone, targetUser, "")
}

func (m *GorokuWeb) PromptCode(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User, errMsg string) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.translator, m.Name(), "code_sent", "The code has been sent. Enter it")
	if errMsg != "" {
		text = fmt.Sprintf("⚠️ <b>%s</b>\n\n%s", errMsg, text)
	}

	btn := inline.Button{
		Text:  getTrans(m.translator, m.Name(), "enter_code", "Enter the code"),
		Input: getTrans(m.translator, m.Name(), "login_code", "Your login code"),
		InputHandler: func(c2 inline.CallbackQuery, code string) error {
			return m.InlineCodeHandler(c2, code, tempClient, phone, targetUser)
		},
	}

	markup := [][]inline.Button{{btn}}
	return c.Edit(text, im.GenerateMarkup(markup))
}

func (m *GorokuWeb) InlineCodeHandler(c inline.CallbackQuery, code string, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User) error {
	code = strings.TrimSpace(code)
	if len(code) != 5 {
		invalidCode := getTrans(m.translator, m.Name(), "invalid_code", "Invalid code. Please try again.")
		return m.PromptCode(c, tempClient, phone, targetUser, invalidCode)
	}

	for _, r := range code {
		if r < '0' || r > '9' {
			return m.PromptCode(c, tempClient, phone, targetUser, "Code must contain digits only.")
		}
	}

	err := tempClient.SignIn(phone, code, "")
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(strings.ToLower(errMsg), "password") || strings.Contains(strings.ToLower(errMsg), "2fa") {
			return m.Prompt2FA(c, tempClient, phone, targetUser, "")
		}
		if strings.Contains(strings.ToLower(errMsg), "expired") {
			expiredTemplate := getTrans(m.translator, m.Name(), "code_expired", "The code has expired.")
			btnRequest := inline.Button{
				Text: getTrans(m.translator, m.Name(), "request_code", "Request code"),
				Handler: func(c2 inline.CallbackQuery) error {
					return m.InlineLogin(c2, targetUser, false)
				},
			}
			return c.Edit(expiredTemplate, c.Manager.GenerateMarkup([][]inline.Button{{btnRequest}}))
		}
		if strings.Contains(strings.ToLower(errMsg), "flood") {
			floodTemplate := getTrans(m.translator, m.Name(), "floodwait_error", "Too many attempts. Try again in {} seconds.")
			errMsg = strings.Replace(floodTemplate, "{}", "many", 1)
			btnNo := inline.Button{
				Text:    getTrans(m.translator, m.Name(), "btn_no", "🔻 Close"),
				Handler: closeForm,
			}
			return c.Edit(fmt.Sprintf("❌ %s", errMsg), c.Manager.GenerateMarkup([][]inline.Button{{btnNo}}))
		}
		invalidCode := getTrans(m.translator, m.Name(), "invalid_code", "Invalid code. Please try again.")
		return m.PromptCode(c, tempClient, phone, targetUser, fmt.Sprintf("%s (%v)", invalidCode, err))
	}

	return m.SuccessLogin(c, tempClient)
}

func (m *GorokuWeb) Prompt2FA(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User, errMsg string) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.translator, m.Name(), "2fa_enabled", "You have two-factor authentication enabled. Enter the password.")
	if errMsg != "" {
		text = fmt.Sprintf("⚠️ <b>%s</b>\n\n%s", errMsg, text)
	}

	btn := inline.Button{
		Text:  getTrans(m.translator, m.Name(), "enter_2fa", "Enter 2FA password"),
		Input: getTrans(m.translator, m.Name(), "your_2fa", "Your two-factor authentication password"),
		InputHandler: func(c2 inline.CallbackQuery, password string) error {
			return m.Inline2FAHandler(c2, password, tempClient, phone, targetUser)
		},
	}

	markup := [][]inline.Button{{btn}}
	return c.Edit(text, im.GenerateMarkup(markup))
}

func (m *GorokuWeb) Inline2FAHandler(c inline.CallbackQuery, password string, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User) error {
	password = strings.TrimSpace(password)
	if password == "" {
		return m.Prompt2FA(c, tempClient, phone, targetUser, "Password cannot be empty.")
	}

	err := tempClient.SignIn(phone, "", password)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(strings.ToLower(errMsg), "flood") {
			floodTemplate := getTrans(m.translator, m.Name(), "floodwait_error", "Too many attempts. Try again in {} seconds.")
			errMsg = strings.Replace(floodTemplate, "{}", "many", 1)
			btnNo := inline.Button{
				Text:    getTrans(m.translator, m.Name(), "btn_no", "🔻 Close"),
				Handler: closeForm,
			}
			return c.Edit(fmt.Sprintf("❌ %s", errMsg), c.Manager.GenerateMarkup([][]inline.Button{{btnNo}}))
		}
		invalidPassword := getTrans(m.translator, m.Name(), "invalid_password", "Invalid password. Please try again.")
		return m.Prompt2FA(c, tempClient, phone, targetUser, fmt.Sprintf("%s (%v)", invalidPassword, err))
	}

	return m.SuccessLogin(c, tempClient)
}

func (m *GorokuWeb) SuccessLogin(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient) error {
	tgID := tempClient.TGID
	tempClient.Disconnect()

	if tgID != 0 {
		baseDir := utils.GetBaseDir()
		oldSession := filepath.Join(baseDir, "goroku-0.session")
		newSession := filepath.Join(baseDir, fmt.Sprintf("goroku-%d.session", tgID))
		if _, err := os.Stat(oldSession); err == nil {
			_ = os.Rename(oldSession, newSession)
		}
	}

	successText := getTrans(m.translator, m.Name(), "login_successful", "🎉 Successful login!")
	_ = c.Edit(fmt.Sprintf("%s\nUserbot will now restart to load the new account.", successText), tgbotapi.InlineKeyboardMarkup{})

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}
