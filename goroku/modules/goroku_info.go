package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

type GorokuInfo struct {
	client        *goroku.CustomTelegramClient
	db            *goroku.Database
	translator    *goroku.Translator
	customMessage string
	bannerURL     string
	pingEmoji     string
	quoteMedia    bool
	invertMedia   bool
	showGoroku    bool
}

func (m *GorokuInfo) Name() string {
	return "GorokuInfo"
}

func (m *GorokuInfo) Strings() map[string]string {
	return map[string]string{
		"name":                "GorokuInfo",
		"up-to-date":          "🟢 <b>Goroku Userbot is up to date</b>",
		"update_required":     "🔴 <b>Goroku Userbot needs update: run</b> <code>{0}update</code>",
		"non_detectable":      "unknown OS",
		"info_message":        "{0}\n<blockquote>┌\n├  【<tg-emoji emoji-id=5883964170268840032>👤</tg-emoji>】 <b>𝙾𝚠ней</b>: {me}\n├  【<tg-emoji emoji-id=5931415565955503486>🤖</tg-emoji>】 <b>𝚅𝚎𝚛𝚜𝚒𝚘𝚗</b>: {version}\n└</blockquote>\n<blockquote>┌\n├  【<tg-emoji emoji-id=5778335621491723621>📷</tg-emoji>】 <b>𝙿𝚛𝚎𝚏𝚒𝚡</b>: {prefix}⠀ ⠀ \n├  【<tg-emoji emoji-id=5843799474362652262>🔄</tg-emoji>】 <b>𝚄𝚙𝚝𝚒𝚖𝚎</b>: {uptime}\n├  【<tg-emoji emoji-id=5879770735999717115>👤</tg-emoji>】 <b>𝙱𝚛𝚊𝚗𝚌𝚑</b>: {branch}\n└</blockquote>\n<blockquote>┌\n├  【<tg-emoji emoji-id=5775887550262546277>❗️</tg-emoji>】 <b>𝙲𝙿𝚄</b>: {cpu_usage}\n├  【<tg-emoji emoji-id=5931409969613116639>🛡</tg-emoji>】 <b>𝚁𝙰𝙼</b>: {ram_usage}\n├  【<tg-emoji emoji-id=5931472654660800739>📊</tg-emoji>】 <b>𝙿𝚒𝚗𝚐</b>: {ping}\n└</blockquote>\n<blockquote>┌\n├  【<tg-emoji emoji-id=5926783847453692661>🛡</tg-emoji>】 <b>𝚄𝚙𝚍𝚊𝚝𝚎</b>: {upd}\n├  【<tg-emoji emoji-id=5931415565955503486>🤖</tg-emoji>】 <b>𝙷𝚘𝚜𝚝</b>: {platform}\n├  【<tg-emoji emoji-id=5819078828017849357>🤖</tg-emoji>】 <b>𝙾𝚂</b>: {os}\n├  【<tg-emoji emoji-id=5819078828017849357>🤖</tg-emoji>】 <b>𝙶𝚘 𝚅𝚎𝚛𝚜𝚒𝚘𝚗</b>: {go_ver}\n└</blockquote>",
		"desc":                "Show userbot info",
		"no_banner":           "🚫 <b>Could not load banner from:</b> {0}",
		"_cfg_custom_message": "Custom template message for info",
		"_cfg_banner_url":     "Banner URL shown in info",
		"_cfg_ping_emoji":     "Ping emoji shown in info",
		"_cfg_quote_media":    "Switch preview media to quote",
		"_cfg_invert_media":   "Invert media position (above/below text)",
		"_cfg_show_goroku":    "Show platform name (e.g. Goroku) if premium",
	}
}

func (m *GorokuInfo) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *GorokuInfo) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"custom_message": "",
		"banner_url":     defaultGorokuInfoBanner(),
		"ping_emoji":     "🪐",
		"quote_media":    false,
		"invert_media":   false,
		"show_goroku":    true,
	}
}

func defaultGorokuInfoBanner() string {
	for _, path := range []string{
		"goroku/assets/goroku_info.png",
		"assets/goroku.png",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (m *GorokuInfo) ConfigReady(config map[string]interface{}) error {
	if val, ok := config["custom_message"].(string); ok {
		m.customMessage = strings.ReplaceAll(val, `\n`, "\n")
	}
	if val, ok := config["banner_url"].(string); ok {
		m.bannerURL = val
	}
	if strings.Contains(m.bannerURL, "coddrago/assets") && (strings.Contains(m.bannerURL, "goroku_info.png") || strings.Contains(m.bannerURL, "heroku_info.png")) {
		m.bannerURL = defaultGorokuInfoBanner()
	}
	if val, ok := config["ping_emoji"].(string); ok {
		m.pingEmoji = val
	}
	if val, ok := config["quote_media"].(bool); ok {
		m.quoteMedia = val
	}
	if val, ok := config["invert_media"].(bool); ok {
		m.invertMedia = val
	}
	if val, ok := config["show_goroku"].(bool); ok {
		m.showGoroku = val
	}
	return nil
}

func (m *GorokuInfo) ClientReady() error { return nil }
func (m *GorokuInfo) OnUnload() error    { return nil }
func (m *GorokuInfo) OnDlmod() error     { return nil }

func (m *GorokuInfo) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"info":   m.InfoCmd,
		"ubinfo": m.UbinfoCmd,
	}
}

func (m *GorokuInfo) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *GorokuInfo) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *GorokuInfo) UbinfoCmd(msg *goroku.Message) error {
	desc := m.getTrans("desc", "Show userbot info")
	return msg.Answer(desc)
}

func getOSName() string {
	content, err := os.ReadFile("/etc/os-release")
	if err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				pretty := strings.TrimPrefix(line, "PRETTY_NAME=")
				pretty = strings.Trim(pretty, `"'`)
				return pretty
			}
		}
	}
	return "Linux"
}

func (m *GorokuInfo) InfoCmd(msg *goroku.Message) error {
	startTime := time.Now()
	var pingMs float64
	pingEmoji := m.pingEmoji
	if pingEmoji == "" {
		pingEmoji = "🪐"
	}

	if m.customMessage != "" && strings.Contains(m.customMessage, "{ping}") {
		_ = msg.Answer(pingEmoji)
	}

	// Fetch platform details
	ramUsage := utils.GetRAMUsage()
	cpuUsage := utils.GetCPUUsage()
	uptime := utils.FormattedUptime()
	platformName := utils.GetPlatformName()
	platformEmoji := utils.GetPlatformEmoji()
	gitStatus := utils.GetGitStatus()
	branch := utils.GetBranch()
	goVersion := runtime.Version()

	// Replace platform emojis with custom tg-emojis
	replacements := map[string]string{
		"🍊":    `<tg-emoji emoji-id="5449599833973203438">🧡</tg-emoji>`,
		"🍇":    `<tg-emoji emoji-id="5449468596952507859">💜</tg-emoji>`,
		"😶‍🌫️": `<tg-emoji emoji-id="5370547013815376328">😶‍🌫️</tg-emoji>`,
		"❓":    `<tg-emoji emoji-id="5407025283456835913">📱</tg-emoji>`,
		"🍀":    `<tg-emoji emoji-id="5395325195542078574">🍀</tg-emoji>`,
		"🦾":    `<tg-emoji emoji-id="5386766919154016047">🦾</tg-emoji>`,
		"🚂":    `<tg-emoji emoji-id="5359595190807962128">🚂</tg-emoji>`,
		"🐳":    `<tg-emoji emoji-id="5431815452437257407">🐳</tg-emoji>`,
		"🕶":    `<tg-emoji emoji-id="5407025283456835913">📱</tg-emoji>`,
		"🐈‍⬛":  `<tg-emoji emoji-id="6334750507294262724">🐈‍⬛</tg-emoji>`,
		"✌️":   `<tg-emoji emoji-id="5469986291380657759">✌️</tg-emoji>`,
		"💎":    `<tg-emoji emoji-id="5471952986970267163">💎</tg-emoji>`,
		"🛡":    `<tg-emoji emoji-id="5282731554135615450">🌩</tg-emoji>`,
		"🌼":    `<tg-emoji emoji-id="5224219153077914783">❤️</tg-emoji>`,
		"🎡":    `<tg-emoji emoji-id="5226711870492126219">🎡</tg-emoji>`,
		"🐧":    `<tg-emoji emoji-id="5361541227604878624">🐧</tg-emoji>`,
		"🧃":    `<tg-emoji emoji-id="5422884965593397853">🧃</tg-emoji>`,
		"🦅":    `<tg-emoji emoji-id="5427286516797831670">🦅</tg-emoji>`,
		"💻":    `<tg-emoji emoji-id="5469825590884310445">💻</tg-emoji>`,
		"🍏":    `<tg-emoji emoji-id="5372908412604525258">🍏</tg-emoji>`,
	}
	for emoji, icon := range replacements {
		platformEmoji = strings.ReplaceAll(platformEmoji, emoji, icon)
	}

	// Get OS details
	osName := getOSName()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	var username string
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" {
		username = os.Getenv("USER")
		if username == "" {
			username = os.Getenv("USERNAME")
			if username == "" {
				username = "unknown"
			}
		}
	}

	// Format update status
	prefix := "."
	if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
		prefix = pVal
	}

	var upd string
	if utils.IsUpToDate() {
		upd = m.getTrans("up-to-date", "🟢 <b>Goroku Userbot is up to date</b>")
	} else {
		updTemplate := m.getTrans("update_required", "🔴 <b>Goroku Userbot needs update: run</b> <code>{0}update</code>")
		upd = formatTrans(updTemplate, prefix)
		upd = strings.ReplaceAll(upd, "{prefix}", utils.EscapeHTML(prefix))
	}

	// Format current user me
	var meStr string
	me, err := m.client.GetMe()
	if err == nil {
		if u, ok := me.(*tg.User); ok {
			displayName := u.FirstName
			if u.LastName != "" {
				displayName += " " + u.LastName
			}
			displayName = utils.EscapeHTML(displayName)
			meStr = fmt.Sprintf("<b><a href=\"tg://user?id=%d\">%s</a></b>", u.ID, displayName)
		}
	}
	if meStr == "" {
		meStr = fmt.Sprintf("<b><a href=\"tg://user?id=%d\">User%d</a></b>", m.client.TGID, m.client.TGID)
	}

	prefixFormat := fmt.Sprintf("«<code>%s</code>»", utils.EscapeHTML(prefix))

	// Platform Premium Emoji status
	var platformPremiumEmoji string
	isPremium := false
	if u, ok := me.(*tg.User); ok {
		isPremium = u.Premium
	}
	if isPremium && m.showGoroku {
		platformPremiumEmoji = utils.GetPlatformEmoji()
	}

	pingMs = float64(time.Since(startTime).Nanoseconds()) / 1e6
	args := map[string]string{
		"{me}":             meStr,
		"{version}":        goroku.GetVersionString(),
		"{prefix}":         prefixFormat,
		"{uptime}":         uptime,
		"{branch}":         branch,
		"{cpu_usage}":      cpuUsage,
		"{ram_usage}":      fmt.Sprintf("%.2f MB", ramUsage),
		"{ping}":           fmt.Sprintf("%.3f", pingMs),
		"{upd}":            upd,
		"{platform}":       utils.EscapeHTML(platformName),
		"{platform_emoji}": platformEmoji,
		"{os}":             utils.EscapeHTML(osName),
		"{hostname}":       utils.EscapeHTML(hostname),
		"{user}":           utils.EscapeHTML(username),
		"{go_ver}":         goVersion,
		"{ping_emoji}":     pingEmoji,
		"{python_ver}":     goVersion,
		"{git_status}":     gitStatus,
	}

	var text string
	if m.customMessage != "" {
		replacer := strings.NewReplacer(
			"{me}", meStr,
			"{version}", goroku.GetVersionString(),
			"{prefix}", prefixFormat,
			"{uptime}", uptime,
			"{branch}", branch,
			"{cpu_usage}", cpuUsage,
			"{ram_usage}", fmt.Sprintf("%.2f MB", ramUsage),
			"{ping}", fmt.Sprintf("%.3f", pingMs),
			"{upd}", upd,
			"{platform}", utils.EscapeHTML(platformName),
			"{platform_emoji}", platformEmoji,
			"{os}", utils.EscapeHTML(osName),
			"{hostname}", utils.EscapeHTML(hostname),
			"{user}", utils.EscapeHTML(username),
			"{go_ver}", goVersion,
			"{ping_emoji}", pingEmoji,
			"{python_ver}", goVersion,
			"{git_status}", gitStatus,
		)
		text = replacer.Replace(m.customMessage)
	} else {
		// Default from translator
		infoTemplate := m.getTrans("info_message", m.Strings()["info_message"])
		text = m.formatInfoMessage(infoTemplate, args, platformPremiumEmoji)
	}

	var opts []goroku.MsgOption
	if m.bannerURL != "" {
		if m.quoteMedia {
			opts = append(opts, goroku.WithWebPageMedia(m.bannerURL, true, true))
			if m.invertMedia {
				opts = append(opts, goroku.WithInvertMedia(true))
			}
			return msg.Answer(text, opts...)
		} else {
			if msg.Out {
				_ = msg.Delete()
			}
			if msg.ReplyToMsgID != 0 {
				opts = append(opts, WithReplyTo(int32(msg.ReplyToMsgID)))
			}
			_, err := m.client.SendFileWithOptions(msg.ChatID, m.bannerURL, text, opts...)
			if err == nil {
				return nil
			}
			// The default banner is hosted outside Telegram and can disappear/404.
			// .info should still work as a text message instead of failing the command.
			opts = []goroku.MsgOption{goroku.WithNoWebpage(true)}
			return msg.Answer(text, opts...)
		}
	} else {
		opts = append(opts, goroku.WithNoWebpage(true))
		return msg.Answer(text, opts...)
	}
}

func (m *GorokuInfo) formatInfoMessage(template string, args map[string]string, firstArg string) string {
	// First replace Python-style positional placeholders.
	template = strings.Replace(template, "{0}", firstArg, 1)
	text := strings.Replace(template, "{}", firstArg, 1)

	// Replace named placeholders
	var pairs []string
	for k, v := range args {
		pairs = append(pairs, k, v)
	}
	replacer := strings.NewReplacer(pairs...)
	return replacer.Replace(text)
}
