package modules

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
)

var latinMock = []string{
	"Amor", "Arbor", "Astra", "Aurum", "Bellum", "Caelum",
	"Calor", "Candor", "Carpe", "Celer", "Certo", "Cibus",
	"Civis", "Clemens", "Coetus", "Cogito", "Conexus",
	"Consilium", "Cresco", "Cura", "Cursus", "Decus",
	"Deus", "Dies", "Digitus", "Discipulus", "Dominus",
	"Donum", "Dulcis", "Durus", "Elementum", "Emendo",
	"Ensis", "Equus", "Espero", "Fidelis", "Fides",
	"Finis", "Flamma", "Flos", "Fortis", "Frater", "Fuga",
	"Fulgeo", "Genius", "Gloria", "Gratia", "Gravis",
	"Habitus", "Honor", "Hora", "Ignis", "Imago",
	"Imperium", "Inceptum", "Infinitus", "Ingenium",
	"Initium", "Intra", "Iunctus", "Iustitia", "Labor",
	"Laurus", "Lectus", "Legio", "Liberi", "Libertas",
	"Lumen", "Lux", "Magister", "Magnus", "Manus",
	"Memoria", "Mens", "Mors", "Mundo", "Natura",
	"Nexus", "Nobilis", "Nomen", "Novus", "Nox",
	"Oculus", "Omnis", "Opus", "Orbis", "Ordo", "Os",
	"Pax", "Perpetuus", "Persona", "Petra", "Pietas",
	"Pons", "Populus", "Potentia", "Primus", "Proelium",
	"Pulcher", "Purus", "Quaero", "Quies", "Ratio",
	"Regnum", "Sanguis", "Sapientia", "Sensus", "Serenus",
	"Sermo", "Signum", "Sol", "Solus", "Sors", "Spes",
	"Spiritus", "Stella", "Summus", "Teneo", "Terra",
	"Tigris", "Trans", "Tribuo", "Tristis", "Ultimus",
	"Unitas", "Universus", "Uterque", "Valde", "Vates",
	"Veritas", "Verus", "Vester", "Via", "Victoria",
	"Vita", "Vox", "Vultus", "Zephyrus", "Bimbalas", "Nywuctuu",
	"Anyone", "Draher", "Hackimo", "Silvyr",
}

type InlineStuff struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *InlineStuff) Name() string {
	return "InlineStuff"
}

func (m *InlineStuff) Strings() map[string]string {
	return map[string]string{
		"name": "InlineStuff",
	}
}

func (m *InlineStuff) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *InlineStuff) ClientReady() error { return nil }
func (m *InlineStuff) OnUnload() error    { return nil }
func (m *InlineStuff) OnDlmod() error     { return nil }

func (m *InlineStuff) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"ch_goroku_bot": m.ChGorokuBotCmd,
		"ch_bot_token":  m.ChBotTokenCmd,
		"inlineinfo":    m.InlineinfoCmd,
	}
}

func (m *InlineStuff) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"ch_bot_token": {
			Aliases: []string{"inlinetoken"},
		},
	}
}

func (m *InlineStuff) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func (m *InlineStuff) checkBot(username string) bool {
	if ok, err := m.client.CheckBot(username); err == nil && ok {
		return true
	}
	if _, err := m.client.ResolvePeer(username); err != nil {
		return true
	}
	return false
}

func (m *InlineStuff) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{
		func(msg *goroku.Message) error {
			if !msg.Out {
				return nil
			}
			im, ok := m.client.GorokuInline.(*inline.InlineManager)
			if !ok || im == nil {
				return nil
			}
			if msg.ViaBotID == im.BotID && strings.Contains(msg.Text, "This message will be deleted automatically") {
				_ = msg.Delete()
			}
			return nil
		},
		func(msg *goroku.Message) error {
			if !msg.Out {
				return nil
			}
			im, ok := m.client.GorokuInline.(*inline.InlineManager)
			if !ok || im == nil {
				return nil
			}
			if msg.ViaBotID != im.BotID || !strings.Contains(msg.Text, "Opening gallery...") {
				return nil
			}

			re := regexp.MustCompile(`#id: ([a-zA-Z0-9]+)`)
			matches := re.FindStringSubmatch(msg.RawText)
			if len(matches) < 2 {
				return nil
			}
			id := matches[1]

			_ = msg.Delete()

			unit, exists := im.GetUnit(id)
			if !exists {
				item, ok := im.PopQueryGallery(id)
				if !ok {
					return nil
				}
				unit = &inline.Unit{
					ID:              id,
					Type:            "gallery",
					Text:            item.Caption,
					ForceMe:         item.ForceMe,
					DisableSecurity: item.DisableSecurity,
				}
				switch nh := item.NextHandler.(type) {
				case []string:
					unit.Pages = nh
				case func(int) string:
					unit.GalleryFn = nh
				default:
					return nil
				}
			}

			var replyTo int64
			if msg.ReplyToMsgID != 0 {
				replyTo = msg.ReplyToMsgID
			}

			respMsgInterface, err := m.client.SendMessageWithOptions(msg.ChatID, "🪐", withReplyTo(int32(replyTo)))
			if err != nil {
				return nil
			}

			var nextHandler interface{}
			if unit.GalleryFn != nil {
				nextHandler = unit.GalleryFn
			} else {
				nextHandler = unit.Pages
			}

			var opts []inline.FormOpt
			if unit.ForceMe {
				opts = append(opts, inline.WithForceMe(true))
			}
			if unit.DisableSecurity {
				opts = append(opts, inline.WithDisableSecurity(true))
			}
			if len(unit.AlwaysAllow) > 0 {
				opts = append(opts, inline.WithAlwaysAllow(unit.AlwaysAllow))
			}

			_, _ = im.Gallery(respMsgInterface, nextHandler, unit.Text, opts...)

			return nil
		},
	}
}

func (m *InlineStuff) ChGorokuBotCmd(msg *goroku.Message) error {
	args := strings.TrimSpace(msg.Text)
	parts := strings.SplitN(args, " ", 2)
	var rawArgs string
	if len(parts) > 1 {
		rawArgs = strings.TrimSpace(parts[1])
	}
	rawArgs = strings.TrimPrefix(rawArgs, "@")

	if rawArgs == "" {
		uid := genRandStr(7)
		genran := strings.ToLower(getRandMock())
		rawArgs = fmt.Sprintf("%s_%s_bot", genran, uid)
	}

	valid := true
	if !strings.HasSuffix(strings.ToLower(rawArgs), "bot") || len(rawArgs) <= 4 {
		valid = false
	} else {
		for _, char := range rawArgs {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
				valid = false
				break
			}
		}
	}

	if !valid {
		msg.Text = m.getTrans("bot_username_invalid", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Specified bot username is invalid. It must end with</b> <code>bot</code> <b>and contain at least 4 symbols</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	if _, err := m.client.ResolvePeer("@" + rawArgs); err == nil {
		if !m.checkBot(rawArgs) {
			msg.Text = m.getTrans("bot_username_occupied", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>This username is already occupied</b>")
			if msg.Client != nil {
				_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
			}
			return nil
		}
	}

	m.db.Set("goroku.inline", "custom_bot", rawArgs)
	m.db.Set("goroku.inline", "bot_token", nil)
	msg.Text = m.getTrans("bot_updated", "<tg-emoji emoji-id=6318792204118656433>🎉</tg-emoji> <b>Config successfully saved. Restart userbot to apply changes</b>")
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *InlineStuff) ChBotTokenCmd(msg *goroku.Message) error {
	parts := strings.SplitN(msg.Text, " ", 2)
	var token string
	if len(parts) > 1 {
		token = strings.TrimSpace(parts[1])
	}

	re := regexp.MustCompile(`^[0-9]{8,10}:[a-zA-Z0-9_-]{34,36}$`)
	if token == "" || !re.MatchString(token) {
		msg.Text = m.getTrans("token_invalid", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Specified bot token is invalid. It must contain 8-10 numbers, </b><code>:</code> <b>and 34-36 symbols</b>")
		if msg.Client != nil {
			_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
		}
		return nil
	}

	m.db.Set("goroku.inline", "bot_token", token)
	msg.Text = m.getTrans("bot_updated", "<tg-emoji emoji-id=6318792204118656433>🎉</tg-emoji> <b>Config successfully saved. Restart userbot to apply changes</b>")
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *InlineStuff) InlineinfoCmd(msg *goroku.Message) error {
	customBot := "not set"
	if val, ok := m.db.Get("goroku.inline", "custom_bot", "").(string); ok && val != "" {
		customBot = "@" + val
	}

	botToken := "not set"
	if val, ok := m.db.Get("goroku.inline", "bot_token", "").(string); ok && val != "" {
		parts := strings.SplitN(val, ":", 2)
		if len(parts) == 2 && len(parts[1]) > 6 {
			botToken = fmt.Sprintf("%s:%s...%s", parts[0], parts[1][:3], parts[1][len(parts[1])-3:])
		} else {
			botToken = "configured"
		}
	}

	msg.Text = fmt.Sprintf("🪐 <b>Inline bot configuration:</b>\n\n• Bot username: <code>%s</code>\n• Bot token: <code>%s</code>", customBot, botToken)
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *InlineStuff) HandleBotPM(msg *tgbotapi.Message) {
	if msg == nil {
		return
	}

	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return
	}

	switch msg.Text {
	case "/start":
		isPremium := false
		me, err := m.client.GetMe()
		if err == nil {
			if u, ok := me.(*tg.User); ok {
				isPremium = u.Premium
			}
		}

		var planetEmoji string
		if isPremium {
			planetEmoji = "<tg-emoji emoji-id=5463379725441341739>🪐</tg-emoji>"
		} else {
			planetEmoji = "🪐"
		}

		platformEmoji := utils.GetPlatformEmoji()
		if platformEmoji == "" {
			platformEmoji = "Goroku"
		}

		captionTemplate := m.getTrans("this_is_goroku", "{} <b>Hi! This is {} — powerful modular Telegram userbot. You can install it to your account!</b>")
		captionText := formatTrans(captionTemplate, planetEmoji, platformEmoji)

		buttons := [][]inline.Button{
			{
				{
					Text: "GitHub",
					URL:  "https://github.com/gemeguardian/Goroku",
				},
			},
			{
				{
					Text: m.getTrans("support_chat_caption", "Support Chat"),
					URL:  "https://t.me/goroku_talks",
				},
			},
		}
		markup := im.GenerateMarkup(buttons)

		photoConfig := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FileURL("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/start_cmd.png"))
		photoConfig.Caption = captionText
		photoConfig.ParseMode = tgbotapi.ModeHTML
		photoConfig.ReplyMarkup = markup

		_, _ = im.GetBotAPI().Send(photoConfig)

	case "/profile":
		if msg.From == nil || msg.From.ID != m.client.TGID {
			return
		}

		prefix := "."
		if pVal, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok {
			prefix = pVal
		}
		ramUsage := fmt.Sprintf("%.2f", utils.GetRAMUsage())
		cpuUsage := utils.GetCPUUsage()
		host := utils.GetPlatformName()

		profileTemplate := m.getTrans("profile_cmd", "ℹ️ Userbot Main Information\n\n<blockquote>• Prefix: {prefix}\n• RAM Usage: {ram_usage} MB\n• CPU Usage: {cpu_usage}%\n• Hosting: {host}</blockquote>")
		captionText := profileTemplate
		captionText = strings.ReplaceAll(captionText, "{prefix}", prefix)
		captionText = strings.ReplaceAll(captionText, "{ram_usage}", ramUsage)
		captionText = strings.ReplaceAll(captionText, "{cpu_usage}", cpuUsage)
		captionText = strings.ReplaceAll(captionText, "{host}", host)

		buttons := [][]inline.Button{
			{
				{
					Text: "Restart",
					Data: "restart_cmd_" + genRandStr(4),
					Handler: func(c inline.CallbackQuery) error {
						_ = c.Edit(m.getTrans("restart", "🔄 Your Goroku is being restarted.."), tgbotapi.InlineKeyboardMarkup{})
						go func() {
							time.Sleep(1 * time.Second)
							goroku.Restart()
						}()
						return nil
					},
				},
			},
			{
				{
					Text: "Reset prefix",
					Data: "reset_prefix_cmd_" + genRandStr(4),
					Handler: func(c inline.CallbackQuery) error {
						m.db.Set("goroku.main", "command_prefix", ".")
						replyMsg := tgbotapi.NewMessage(c.ChatID, m.getTrans("prefix_reset", "🔀 Prefix reset!"))
						_, _ = im.GetBotAPI().Send(replyMsg)
						return nil
					},
				},
			},
		}
		markup := im.GenerateMarkup(buttons)

		photoConfig := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FileURL("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/start_cmd.png"))
		photoConfig.Caption = captionText
		photoConfig.ParseMode = tgbotapi.ModeHTML
		photoConfig.ReplyMarkup = markup

		_, _ = im.GetBotAPI().Send(photoConfig)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func genRandStr(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz1234567890"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}
	return string(b)
}

func getRandMock() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return latinMock[r.Intn(len(latinMock))]
}

func withReplyTo(replyToMsgID int32) goroku.MsgOption {
	return func(req interface{}) {
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			replyObj := &tg.InputReplyToMessage{
				ReplyToMsgID: int(replyToMsgID),
			}
			r.ReplyTo = replyObj
		}
	}
}
