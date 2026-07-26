package modules

import (
	"bytes"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"math/rand"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

type Test struct {
	goroku.Base
	// Configs
	forceSendAll        bool
	tglogLevel          string
	ignoreCommon        bool
	disableInternetWarn bool
	customMessage       string
	hint                string
	pingEmoji           string
	bannerUrl           string
	quoteMedia          bool
	invertMedia         bool
}

func (m *Test) Name() string {
	return "Tester"
}

func (m *Test) Strings() map[string]string {
	return map[string]string{
		"name":                       "Tester Module",
		"_cfg_force_send_all":        "Force sending all logs to user log chat",
		"_cfg_tglog_level":           "Minimal log level to send to Telegram (DEBUG, INFO, etc.)",
		"_cfg_ignore_common":         "Ignore common logs from being sent to log chat",
		"_cfg_disable_internet_warn": "Ignore all internet errors",
		"_cfg_custom_message":        "Custom ping template message",
		"_cfg_hint":                  "Hint shown at the bottom of ping result",
		"_cfg_ping_emoji":            "Ping animation emoji",
		"_cfg_banner_url":            "Banner URL shown in ping",
		"_cfg_quote_media":           "Switch preview media to quote in ping",
		"_cfg_invert_media":          "Switch preview invert media in ping",
	}
}

func (m *Test) ClientReady() error {
	logChatID := m.Client.GetLogChatID()
	if logChatID != 0 && goroku.TGLogHandler != nil {
		goroku.TGLogHandler.InstallTGLog(m.Client, logChatID)
	}
	return nil
}
func (m *Test) OnDlmod() error { return nil }

var _ goroku.ModuleWithConfigSchema = (*Test)(nil)

// ConfigSchema is the M7 typed config surface for Tester.
func (m *Test) ConfigSchema() []goroku.ConfigField {
	return []goroku.ConfigField{
		{Key: "force_send_all", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
		{Key: "tglog_level", Type: "choice", Default: "ERROR", Validator: &goroku.ChoiceValidator{PossibleValues: []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL", "ALL"}}},
		{Key: "ignore_common", Type: "bool", Default: true, Validator: &goroku.BooleanValidator{}},
		{Key: "disable_internet_warn", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
		{Key: "custom_message", Type: "string", Default: "<tg-emoji emoji-id=5920515922505765329>⚡️</tg-emoji> <b>𝙿𝚒𝚗𝚐: </b><code>{ping}</code><b> 𝚖𝚜 </b>\n<tg-emoji emoji-id=5900104897885376843>🕓</tg-emoji><b> 𝚄𝚙𝚝𝚒𝚖𝚎: </b><code>{uptime}</code>", Validator: &goroku.StringValidator{}},
		{Key: "hint", Type: "string", Default: "", Validator: &goroku.StringValidator{}},
		{Key: "ping_emoji", Type: "string", Default: "🪐", Validator: &goroku.StringValidator{}},
		{Key: "banner_url", Type: "string", Default: "", Validator: &goroku.StringValidator{}},
		{Key: "quote_media", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
		{Key: "invert_media", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
	}
}

func (m *Test) ConfigReady(config map[string]any) error {
	if val, ok := config["force_send_all"].(bool); ok {
		m.forceSendAll = val
	}
	if val, ok := config["tglog_level"].(string); ok {
		m.tglogLevel = val
	}
	if val, ok := config["ignore_common"].(bool); ok {
		m.ignoreCommon = val
	}
	if val, ok := config["disable_internet_warn"].(bool); ok {
		m.disableInternetWarn = val
	}
	if val, ok := config["custom_message"].(string); ok {
		m.customMessage = strings.ReplaceAll(val, `\n`, "\n")
	}
	if val, ok := config["hint"].(string); ok {
		m.hint = val
	}
	if val, ok := config["ping_emoji"].(string); ok {
		m.pingEmoji = val
	}
	if val, ok := config["banner_url"].(string); ok {
		m.bannerUrl = val
	}
	if val, ok := config["quote_media"].(bool); ok {
		m.quoteMedia = val
	}
	if val, ok := config["invert_media"].(bool); ok {
		m.invertMedia = val
	}
	goroku.SetZapLogLevel(m.tglogLevel)
	return nil
}

func (m *Test) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"ping":      m.PingCmd,
		"clearlogs": m.ClearLogsCmd,
		"suspend":   m.SuspendCmd,
		"logs":      m.LogsCmd,
	}
}

func formatCustomMessage(tmpl string, data map[string]string) string {
	res := tmpl
	for k, v := range data {
		res = strings.ReplaceAll(res, "{"+k+"}", v)
	}
	return res
}

func getUsername() string {
	u, err := user.Current()
	if err == nil {
		return u.Username
	}
	return ""
}

func (m *Test) makeButton(text string, handler func(inline.CallbackQuery) error) inline.Button {
	return inline.Button{
		Text:    text,
		Data:    fmt.Sprintf("tst_%d_%d", time.Now().UnixNano(), rand.Int63()), //nolint:gosec
		Handler: handler,
	}
}

// PingCmd measures round-trip latency, then reports ping/uptime/platform/hostname.
func (m *Test) PingCmd(msg *goroku.Message) error {
	startNs := time.Now()

	emoji := m.pingEmoji
	if emoji == "" {
		emoji = "🪐"
	}

	var targetMsgID int64
	if msg.Out {
		_ = msg.Edit(emoji)
		targetMsgID = msg.ID
	} else {
		sentMsg, err := m.Client.SendMessage(goroku.ChatRefID(msg.ChatID), emoji)
		if err == nil {
			targetMsgID = sentMsg.SentMessageID()
		}
	}

	pingMs := float64(time.Since(startNs).Nanoseconds()) / 1e6
	uptime := utils.FormattedUptime()
	platform := utils.GetPlatformName()
	hostname, _ := os.Hostname()

	pingHint := ""
	if m.hint != "" && rand.Intn(3) == 2 { //nolint:gosec
		pingHint = m.hint
	}

	data := map[string]string{
		"ping":      fmt.Sprintf("%.3f", pingMs),
		"uptime":    uptime,
		"ping_hint": pingHint,
		"hostname":  hostname,
		"platform":  platform,
		"user":      getUsername(),
	}

	tmpl := m.customMessage
	if tmpl == "" {
		tmpl = "<tg-emoji emoji-id=5920515922505765329>⚡️</tg-emoji> <b>𝙿𝚒𝚗𝚐: </b><code>{ping}</code><b> 𝚖𝚜 </b>\n<tg-emoji emoji-id=5900104897885376843>🕓</tg-emoji><b> 𝚄𝚙𝚝𝚒𝚖𝚎: </b><code>{uptime}</code>"
	}

	response := formatCustomMessage(tmpl, data)
	if targetMsgID != 0 {
		_, err := m.Client.EditMessage(goroku.ChatRefID(msg.ChatID), targetMsgID, response)
		return err
	}
	return msg.Answer(response)
}

// ClearLogsCmd clears the in-memory log buffer.
func (m *Test) ClearLogsCmd(msg *goroku.Message) error {
	if goroku.TGLogHandler != nil {
		goroku.TGLogHandler.Clear()
	}
	msg.Text = m.T("logs_cleared", "🗑 <b>Logs cleared</b>")
	return nil
}

// SuspendCmd suspends the bot for the given number of seconds (max 100 years).
func (m *Test) SuspendCmd(msg *goroku.Message) error {
	raw := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))
	timeSleep, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		msg.Text = m.T("suspend_invalid_time", "❌ Invalid time")
		return nil
	}

	if timeSleep > 86400*365*100 {
		msg.Text = m.T("suspend_invalid_time", "❌ Invalid time")
		return nil
	}

	trans := m.T("suspended", "Suspended for {} seconds")
	msg.Text = formatTrans(trans, fmt.Sprintf("%g", timeSleep))
	time.Sleep(time.Duration(timeSleep * float64(time.Second)))
	return nil
}

func (m *Test) LogsCmd(msg *goroku.Message) error {
	rawArgs := strings.TrimSpace(utils.GetArgsRaw(msg.RawText))

	var lvl int = -1
	if rawArgs != "" {
		parts := strings.Fields(rawArgs)
		arg := strings.ToLower(parts[0])

		if val, err := strconv.Atoi(arg); err == nil {
			lvl = val
		} else {
			switch arg {
			case "critical":
				lvl = 60
			case "error":
				lvl = 40
			case "warning", "warn":
				lvl = 30
			case "info":
				lvl = 20
			case "debug":
				lvl = 10
			case "all":
				lvl = 0
			}
		}
	}

	im := m.Client.GorokuInline
	if lvl == -1 {
		if im != nil && im.IsComplete() {
			markup := [][]inline.Button{
				{
					m.makeButton("🚫 Critical", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 60, false)
					}),
					m.makeButton("🚫 Error", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 40, false)
					}),
				},
				{
					m.makeButton("⚠️ Warning", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 30, false)
					}),
					m.makeButton("ℹ️ Info", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 20, false)
					}),
				},
				{
					m.makeButton("⚠️ Debug", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 10, false)
					}),
					m.makeButton("🧑‍💻 All", func(call inline.CallbackQuery) error {
						return m.SendLogs(call, 0, false)
					}),
				},
				{
					{
						Text: m.T("cancel", "🚫 Cancel"),
						Handler: func(call inline.CallbackQuery) error {
							return closeForm(call)
						},
					},
				},
			}
			chooseText := m.T("choose_loglevel", "💁‍♂️ <b>Choose log level</b>")
			_, err := im.Form(chooseText, msg, markup)
			return err
		}
		lvl = 0
	}

	return m.SendLogs(msg, lvl, false)
}

func (m *Test) SendLogs(msg any, lvl int, force bool) error {
	namedLvl := strconv.Itoa(lvl)
	switch lvl {
	case 60:
		namedLvl = "CRITICAL"
	case 40:
		namedLvl = "ERROR"
	case 30:
		namedLvl = "WARNING"
	case 20:
		namedLvl = "INFO"
	case 10:
		namedLvl = "DEBUG"
	case 0:
		namedLvl = "ALL"
	}

	rawText := ""
	if msgObj, ok := msg.(*goroku.Message); ok {
		rawText = msgObj.RawText
	}

	if lvl < 30 && !force && !strings.Contains(strings.ToLower(rawText), "force_insecure") {
		im := m.Client.GorokuInline
		if im != nil && im.IsComplete() {
			markup := [][]inline.Button{
				{
					m.makeButton(m.T("send_anyway", "📤 Send anyway"), func(call inline.CallbackQuery) error {
						return m.SendLogs(call, lvl, true)
					}),
					{
						Text: m.T("cancel", "🚫 Cancel"),
						Handler: func(call inline.CallbackQuery) error {
							return closeForm(call)
						},
					},
				},
			}
			warnText := formatTrans(m.T("confidential_text", "⚠️ <b>Log level</b> <code>{0}</code> <b>may reveal your confidential info, be careful</b>\n<b>Type</b> <code>.logs {0} force_insecure</code> <b>to ignore this warning</b>"), namedLvl)

			var err error
			if msgObj, ok := msg.(*goroku.Message); ok {
				_, err = im.Form(warnText, msgObj, markup)
			} else if callObj, ok := msg.(inline.CallbackQuery); ok {
				err = callObj.Edit(warnText, im.GenerateMarkup(markup))
			}
			return err
		}
	}

	if goroku.TGLogHandler == nil {
		return fmt.Errorf("TGLogHandler not initialized")
	}

	allLines := goroku.TGLogHandler.Dump()
	filteredLines := filterLogsByLevel(allLines, lvl)

	if callObj, ok := msg.(inline.CallbackQuery); ok {
		_ = closeForm(callObj)
	}

	if len(filteredLines) == 0 {
		var err error
		noLogsText := formatTrans(m.T("no_logs", "🤷‍♀️ <b>You don't have any logs at verbosity</b> <code>{}</code><b>.</b>"), namedLvl)
		if msgObj, ok := msg.(*goroku.Message); ok {
			err = msgObj.Answer(noLogsText)
		} else if callObj, ok := msg.(inline.CallbackQuery); ok {
			err = callObj.Answer(noLogsText, true)
		}
		return err
	}

	logsText := strings.Join(filteredLines, "")
	censoredLogs := utils.Censor(logsText)

	var chatID int64
	if msgObj, ok := msg.(*goroku.Message); ok {
		chatID = msgObj.ChatID
	} else if callObj, ok := msg.(inline.CallbackQuery); ok {
		chatID = callObj.ChatID
	}

	filename := "goroku-logs.txt"

	ver := goroku.Version
	hash := utils.GetGitHash()
	gitLink := ""
	if hash != "" {
		gitLink = fmt.Sprintf(" <a href=\"https://github.com/gemeguardian/Goroku/commit/%s\">@%s</a>", hash, hash[:8])
	}
	caption := formatTrans(
		m.T("logs_caption", "📋 <b>Goroku logs with verbosity</b> <code>{0}</code>\n\n⚪️ <b>Version: {1}.{2}.{3}</b>{4}"),
		namedLvl,
		strconv.Itoa(ver[0]),
		strconv.Itoa(ver[1]),
		strconv.Itoa(ver[2]),
		gitLink,
	)

	nr := &namedReader{r: bytes.NewReader([]byte(censoredLogs)), name: filename}
	_, err := m.Client.SendFile(goroku.ChatRefID(chatID), nr, caption)
	return err
}

// filterLogsByLevel filters log lines by minimum severity level.
// Level mapping: 60=CRITICAL, 40=ERROR, 30=WARNING, 20=INFO, 10=DEBUG, 0=ALL.
func filterLogsByLevel(lines []string, lvl int) []string {
	if lvl == 0 {
		return lines
	}
	filtered := []string{}
	for _, line := range lines {
		lineLower := strings.ToLower(line)
		var matches bool
		switch lvl {
		case 60:
			matches = strings.Contains(lineLower, "critical") || strings.Contains(lineLower, "panic") || strings.Contains(lineLower, "fatal")
		case 40:
			matches = strings.Contains(lineLower, "error") || strings.Contains(lineLower, "err") || strings.Contains(lineLower, "critical") || strings.Contains(lineLower, "panic") || strings.Contains(lineLower, "fatal")
		case 30:
			matches = strings.Contains(lineLower, "warning") || strings.Contains(lineLower, "warn") || strings.Contains(lineLower, "error") || strings.Contains(lineLower, "err") || strings.Contains(lineLower, "critical") || strings.Contains(lineLower, "panic") || strings.Contains(lineLower, "fatal")
		case 20:
			matches = strings.Contains(lineLower, "info") || strings.Contains(lineLower, "warning") || strings.Contains(lineLower, "warn") || strings.Contains(lineLower, "error") || strings.Contains(lineLower, "err") || strings.Contains(lineLower, "critical") || strings.Contains(lineLower, "panic") || strings.Contains(lineLower, "fatal")
		default:
			matches = true
		}
		if matches {
			filtered = append(filtered, line)
		}
	}
	return filtered
}
