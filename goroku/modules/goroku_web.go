package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
)

type GorokuWeb struct {
	goroku.Base
}

func (m *GorokuWeb) Name() string {
	return "GorokuWeb"
}

func (m *GorokuWeb) Strings() map[string]string {
	return map[string]string{
		"name": "Goroku Web Module",
	}
}

func (m *GorokuWeb) OnUnload() error { return nil }

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

func (m *GorokuWeb) webRuntime() goroku.WebRuntime {
	if m == nil || m.Client == nil {
		return nil
	}
	return m.Client.Web
}

func (m *GorokuWeb) WebrestartCmd(msg *goroku.Message) error {
	if m.webRuntime() == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	port := 8080
	if currentPort := m.webRuntime().Port(); currentPort != 0 {
		port = currentPort
	}

	m.webRuntime().Stop()
	m.webRuntime().StartAsync(port, true)

	msg.Text = "🔄 <b>Web server restarted.</b>"
	_ = msg.Answer(msg.Text)
	return nil
}

func (m *GorokuWeb) WebpanelCmd(msg *goroku.Message) error {
	if m.webRuntime() == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	im := m.Client.GorokuInline
	hasInline := im != nil && im.IsComplete()

	force := strings.Contains(strings.ToLower(msg.Text), "force_insecure")

	if !force && !msg.IsPrivate {
		privacyLeakNowarn := m.T("privacy_leak_nowarn", "⚠️ <b>WARNING! Sending link to the public chat will compromise your session!</b>\n\nYour user ID is <code>{}</code>. If you are sure you want to get the link here, press button below.")
		privacyLeak := m.T("privacy_leak", "⚠️ <b>WARNING! Sending link to the public chat will compromise your session!</b>\n\nYour user ID is <code>{}</code>. If you are sure you want to get the link here, send <code>{}weburl force_insecure</code>.")

		prefix := m.DB.GetString("goroku.main", "command_prefix", ".")

		if hasInline {
			text := formatTrans(privacyLeakNowarn, fmt.Sprintf("%d", m.Client.TGID))
			btnYes := inline.Button{
				Text: m.T("btn_yes", "🚸 Confirm anyway"),
				Handler: func(call inline.CallbackQuery) error {
					return m.showWebpanelTunnel(call, true)
				},
			}
			btnNo := inline.Button{
				Text:    m.T("btn_no", "🔻 Close"),
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
			text := formatTrans(privacyLeak, fmt.Sprintf("%d", m.Client.TGID), prefix)
			return msg.Answer(text)
		}
	}

	return m.showWebpanelTunnelMessage(msg)
}

func (m *GorokuWeb) showWebpanelTunnelMessage(msg *goroku.Message) error {
	im := m.Client.GorokuInline
	hasInline := im != nil && im.IsComplete()

	openingText := m.T("opening_tunnel", "🕔 <b>Opening tunnel...</b>")
	waitBtn := inline.Button{
		Text: "🕔 Wait...",
		Data: "empty",
		Handler: func(c inline.CallbackQuery) error {
			return c.Answer("Please wait, the tunnel is opening...", false)
		},
	}
	waitMarkup := [][]inline.Button{{waitBtn}}

	if !hasInline {
		_ = msg.Answer(openingText)
		url := m.webRuntime().GetURL(true)
		openedText := m.T("tunnel_opened", "✅ <b>Tunnel opened successfully!</b>")
		webBtnText := m.T("web_btn", "🔗 Web Panel")
		return msg.Answer(fmt.Sprintf("%s\n\n<a href=\"%s\">%s</a>", openedText, url, webBtnText))
	}

	inlineMsg, err := im.Form(
		openingText,
		msg,
		waitMarkup,
		inline.WithPhoto("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/opening_tunnel.png"),
	)
	if err != nil {
		return err
	}

	go func() {
		url := m.webRuntime().GetURL(true)
		openedText := m.T("tunnel_opened", "✅ <b>Tunnel opened successfully!</b>")
		webBtnText := m.T("web_btn", "🔗 Web Panel")
		linkBtn := inline.Button{
			Text: webBtnText,
			URL:  url,
		}
		linkMarkup := [][]inline.Button{{linkBtn}}
		if inlineMsg != nil {
			_ = inlineMsg.Edit(openedText, im.GenerateMarkup(linkMarkup))
		}
	}()
	return nil
}

func (m *GorokuWeb) showWebpanelTunnel(call inline.CallbackQuery, _ bool) error {
	im := m.Client.GorokuInline
	if im == nil || !im.IsComplete() {
		return nil
	}

	openingText := m.T("opening_tunnel", "🕔 <b>Opening tunnel...</b>")
	waitBtn := inline.Button{
		Text: "🕔 Wait...",
		Data: "empty",
		Handler: func(c inline.CallbackQuery) error {
			return c.Answer("Please wait, the tunnel is opening...", false)
		},
	}
	waitMarkup := [][]inline.Button{{waitBtn}}
	if err := call.Edit(openingText, im.GenerateMarkup(waitMarkup)); err != nil {
		return err
	}

	go func() {
		url := m.webRuntime().GetURL(true)
		openedText := m.T("tunnel_opened", "✅ <b>Tunnel opened successfully!</b>")
		webBtnText := m.T("web_btn", "🔗 Web Panel")
		linkBtn := inline.Button{
			Text: webBtnText,
			URL:  url,
		}
		linkMarkup := [][]inline.Button{{linkBtn}}
		_ = call.Edit(openedText, im.GenerateMarkup(linkMarkup))
	}()
	return nil
}

func (m *GorokuWeb) WebstopCmd(msg *goroku.Message) error {
	if m.webRuntime() == nil {
		msg.Text = "❌ <b>Web server is not running or disabled.</b>"
		_ = msg.Answer(msg.Text)
		return nil
	}

	m.webRuntime().Stop()
	msg.Text = "🛑 <b>Web server stopped.</b>"
	_ = msg.Answer(msg.Text)
	return nil
}

func (m *GorokuWeb) ApproveWebCmd(msg *goroku.Message) error {
	if m.webRuntime() == nil {
		return msg.Answer("❌ <b>Web server is not running or disabled.</b>")
	}

	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		return msg.Answer("❌ <b>Please specify the authorization token:</b> <code>.approve_web &lt;token&gt;</code>")
	}

	token := parts[1]
	if m.webRuntime().ApproveWebAuth(token) {
		return msg.Answer("✅ <b>Web Dashboard Authorization Approved!</b>")
	}

	return msg.Answer("❌ <b>Invalid or expired token.</b>")
}

func (m *GorokuWeb) AddaccCmd(msg *goroku.Message) error {
	var targetUser *tg.User
	var targetID int64

	args := utils.GetArgs(msg.Text)
	if len(args) > 0 {
		if full, err := m.Client.GetFullUser(args[0], 0, false); err == nil {
			if u, ok := userClassFromFull(full).(*tg.User); ok {
				targetUser = u
				targetID = u.ID
			}
		}
	} else {
		reply, err := msg.GetReplyMessage()
		if err == nil && reply != nil {
			targetID = reply.SenderID
			if full, err := m.Client.GetFullUser(targetID, 0, false); err == nil {
				if u, ok := userClassFromFull(full).(*tg.User); ok {
					targetUser = u
				}
			}
		}
	}

	if targetUser == nil || targetUser.Bot {
		template := getTrans(m.Translator, m.Name(), "invalid_target", "Reply to the message of the person you want to add, or specify their correct @username/id.")
		_ = msg.Answer(template)
		return nil
	}

	if targetID == m.Client.TGID {
		template := getTrans(m.Translator, m.Name(), "cant_add_self", "You can't add yourself.")
		_ = msg.Answer(template)
		return nil
	}

	forceInsecure := strings.Contains(strings.ToLower(msg.Text), "force_insecure")
	if forceInsecure {
		return m.InlineLogin(msg, targetUser, false)
	}

	prefix := m.DB.GetString("goroku.main", "command_prefix", ".")

	im := m.Client.GorokuInline
	if im == nil || !im.IsComplete() {
		template := getTrans(m.Translator, m.Name(), "add_user_insecure", "Do you really want to add an account {} ({})? Use the <code>{}addacc {} force_insecure</code> command to confirm.")
		text := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID), prefix, fmt.Sprintf("%d", targetID))
		_ = msg.Answer(text)
		return nil
	}

	// Send confirmation form
	template := getTrans(m.Translator, m.Name(), "add_user_confirm", "Do you really want to add an account {} ({})?")
	text := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID))

	btnYes := inline.Button{
		Text: getTrans(m.Translator, m.Name(), "btn_yes", "🚸 Confirm anyway"),
		Handler: func(call inline.CallbackQuery) error {
			return m.InlineLoginCallback(call, targetUser)
		},
	}
	btnNo := inline.Button{
		Text:    getTrans(m.Translator, m.Name(), "btn_no", "🔻 Close"),
		Handler: closeForm,
	}

	markup := [][]inline.Button{{btnYes, btnNo}}
	_, err := im.Form(text, msg, markup)
	if err != nil {
		template := getTrans(m.Translator, m.Name(), "add_user_insecure", "Do you really want to add an account {} ({})? Use the <code>{}addacc {} force_insecure</code> command to confirm.")
		fallbackText := formatTrans(template, targetUser.FirstName, fmt.Sprintf("%d", targetID), prefix, fmt.Sprintf("%d", targetID))
		_ = msg.Answer(fallbackText)
	}

	return nil
}

func (m *GorokuWeb) InlineLoginCallback(call inline.CallbackQuery, targetUser *tg.User) error {
	return m.InlineLogin(call, targetUser, false)
}

func (m *GorokuWeb) InlineLogin(call any, targetUser *tg.User, afterFail bool) error {
	im := m.Client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.Translator, m.Name(), "enter_number_format", "Enter your phone number in the international format (for example, +79212345678):")
	if afterFail {
		incorrect := getTrans(m.Translator, m.Name(), "incorrect_number", "You entered an incorrect phone number.\n\n")
		text = incorrect + text
	}

	btn := inline.Button{
		Text:  getTrans(m.Translator, m.Name(), "enter_number", "Enter the number"),
		Input: getTrans(m.Translator, m.Name(), "your_phone_number", "Your phone number"),
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

func (m *GorokuWeb) floodWaitMessage() string {
	floodTemplate := getTrans(m.Translator, m.Name(), "floodwait_error", "Too many attempts. Try again in {} seconds.")
	return strings.Replace(floodTemplate, "{}", "many", 1)
}

func (m *GorokuWeb) editClosableError(c inline.CallbackQuery, text string) error {
	btnNo := inline.Button{
		Text:    getTrans(m.Translator, m.Name(), "btn_no", "🔻 Close"),
		Handler: closeForm,
	}
	return c.Edit(fmt.Sprintf("❌ %s", text), c.Manager.GenerateMarkup([][]inline.Button{{btnNo}}))
}

func (m *GorokuWeb) InlinePhoneHandler(c inline.CallbackQuery, data string, targetUser *tg.User) error {
	phone := parsePhone(data)
	if phone == "" {
		return m.InlineLogin(c, targetUser, true)
	}

	tempClient := goroku.NewCustomTelegramClient(0)
	tempClient.APIID = m.Client.APIID
	tempClient.APIHash = m.Client.APIHash

	err := tempClient.Connect()
	if err != nil {
		return c.Edit(fmt.Sprintf("❌ Connection failed: %v", err), tgbotapi.InlineKeyboardMarkup{})
	}

	err = tempClient.SendCodeRequest(phone)
	if err != nil {
		_ = tempClient.Disconnect()
		errMsg := err.Error()
		if strings.Contains(strings.ToLower(errMsg), "flood") {
			errMsg = m.floodWaitMessage()
		}
		return m.editClosableError(c, errMsg)
	}

	return m.PromptCode(c, tempClient, phone, targetUser, "")
}

func (m *GorokuWeb) PromptCode(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User, errMsg string) error {
	im := m.Client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.Translator, m.Name(), "code_sent", "The code has been sent. Enter it")
	if errMsg != "" {
		text = fmt.Sprintf("⚠️ <b>%s</b>\n\n%s", errMsg, text)
	}

	btn := inline.Button{
		Text:  getTrans(m.Translator, m.Name(), "enter_code", "Enter the code"),
		Input: getTrans(m.Translator, m.Name(), "login_code", "Your login code"),
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
		invalidCode := getTrans(m.Translator, m.Name(), "invalid_code", "Invalid code. Please try again.")
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
			expiredTemplate := getTrans(m.Translator, m.Name(), "code_expired", "The code has expired.")
			btnRequest := inline.Button{
				Text: getTrans(m.Translator, m.Name(), "request_code", "Request code"),
				Handler: func(c2 inline.CallbackQuery) error {
					return m.InlineLogin(c2, targetUser, false)
				},
			}
			return c.Edit(expiredTemplate, c.Manager.GenerateMarkup([][]inline.Button{{btnRequest}}))
		}
		if strings.Contains(strings.ToLower(errMsg), "flood") {
			return m.editClosableError(c, m.floodWaitMessage())
		}
		invalidCode := getTrans(m.Translator, m.Name(), "invalid_code", "Invalid code. Please try again.")
		return m.PromptCode(c, tempClient, phone, targetUser, fmt.Sprintf("%s (%v)", invalidCode, err))
	}

	return m.SuccessLogin(c, tempClient)
}

func (m *GorokuWeb) Prompt2FA(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient, phone string, targetUser *tg.User, errMsg string) error {
	im := m.Client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := getTrans(m.Translator, m.Name(), "2fa_enabled", "You have two-factor authentication enabled. Enter the password.")
	if errMsg != "" {
		text = fmt.Sprintf("⚠️ <b>%s</b>\n\n%s", errMsg, text)
	}

	btn := inline.Button{
		Text:  getTrans(m.Translator, m.Name(), "enter_2fa", "Enter 2FA password"),
		Input: getTrans(m.Translator, m.Name(), "your_2fa", "Your two-factor authentication password"),
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
			return m.editClosableError(c, m.floodWaitMessage())
		}
		invalidPassword := getTrans(m.Translator, m.Name(), "invalid_password", "Invalid password. Please try again.")
		return m.Prompt2FA(c, tempClient, phone, targetUser, fmt.Sprintf("%s (%v)", invalidPassword, err))
	}

	return m.SuccessLogin(c, tempClient)
}

func (m *GorokuWeb) SuccessLogin(c inline.CallbackQuery, tempClient *goroku.CustomTelegramClient) error {
	tgID := tempClient.TGID
	_ = tempClient.Disconnect()

	if tgID != 0 {
		baseDir := utils.GetBaseDir()
		oldSession := filepath.Join(baseDir, "goroku-0.session")
		newSession := filepath.Join(baseDir, fmt.Sprintf("goroku-%d.session", tgID))
		if _, err := os.Stat(oldSession); err == nil {
			_ = os.Rename(oldSession, newSession)
		}
	}

	successText := getTrans(m.Translator, m.Name(), "login_successful", "🎉 Successful login!")
	_ = c.Edit(fmt.Sprintf("%s\nUserbot will now restart to load the new account.", successText), tgbotapi.InlineKeyboardMarkup{})

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}
