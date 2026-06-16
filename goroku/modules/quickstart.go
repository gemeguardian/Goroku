package modules

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
)

type Quickstart struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func (m *Quickstart) Name() string {
	return "Quickstart"
}

func (m *Quickstart) Strings() map[string]string {
	return map[string]string{
		"name": "Quickstart",
	}
}

func (m *Quickstart) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Quickstart) ClientReady() error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Quickstart ClientReady panic recovered: %v\n", r)
			}
		}()

		var contentChannel interface{}
		var finalCid int64
		existingChannelIDVal := m.db.Get("goroku.forums", "channel_id", nil)
		if existingChannelIDVal != nil {
			var cid int64
			switch v := existingChannelIDVal.(type) {
			case float64:
				cid = int64(v)
			case int64:
				cid = v
			case int:
				cid = int64(v)
			}
			if cid != 0 {
				if cid > 0 {
					cid = goroku.TelegramChannelChatID(cid)
					m.db.Set("goroku.forums", "channel_id", cid)
					m.db.Save()
				}
				peer, err := m.client.ResolvePeer(cid)
				if err == nil {
					contentChannel = peer
					finalCid = cid
				}
			}
		}

		if contentChannel == nil {
			peer, err := m.client.FindChannelByTitle("goroku-userbot")
			if err == nil {
				contentChannel = peer
				var cid int64
				if ch, ok := peer.(*tg.InputPeerChannel); ok {
					cid = goroku.TelegramChannelChatID(ch.ChannelID)
				}
				m.db.Set("goroku.forums", "channel_id", cid)
				finalCid = cid
			}
		}

		if contentChannel == nil {
			peer, isNew := utils.AssetChannel(
				m.client,
				"goroku-userbot",
				"🪐 Content related to Goroku will be here",
				false, // channel
				true,  // silent
				false, // archive
				true,  // inviteBot
				"https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/goroku.png",
				0,    // ttl
				true, // forum
				true, // hideGeneral
				"goroku",
			)
			if peer != nil {
				contentChannel = peer
				var cid int64
				if ch, ok := peer.(*tg.InputPeerChannel); ok {
					cid = goroku.TelegramChannelChatID(ch.ChannelID)
				}
				m.db.Set("goroku.forums", "channel_id", cid)
				finalCid = cid
				_ = isNew
			}
		}

		if contentChannel == nil {
			log.Println("Quickstart: failed to get or create content channel")
			return
		}

		requiredTopics := []struct {
			Title string
			Desc  string
			Emoji int64
		}{
			{"Assets", "🌆 Your Goroku assets will be stored here", 5877307202888273539},
			{"Logs", "📊 Inline logs and error reports will be stored here", 5877307202888273539},
			{"Backups", "💾 Your Goroku backups will be stored here", 5877307202888273539},
		}

		for _, topic := range requiredTopics {
			_, err := utils.AssetForumTopic(
				m.client,
				m.db,
				contentChannel,
				topic.Title,
				topic.Desc,
				topic.Emoji,
				false,
			)
			if err != nil {
				log.Printf("Quickstart: failed to create forum topic %s: %v\n", topic.Title, err)
			}
		}

		_ = finalCid

		// Welcome message with language selector
		sentMsg, ok := m.db.Get("Quickstart", "no_msg", false).(bool)
		if !ok || !sentMsg {
			im, ok := m.client.GorokuInline.(*inline.InlineManager)
			if ok && im != nil {
				for i := 0; i < 20; i++ {
					if im.IsComplete() {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
				if im.IsComplete() {
					m.db.Set("Quickstart", "no_msg", true)
					m.db.Save()
					_ = m.sendMenu(m.client.TGID)
				}
			}
		}
	}()

	return nil
}

func (m *Quickstart) OnUnload() error { return nil }
func (m *Quickstart) OnDlmod() error  { return nil }

func (m *Quickstart) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"quickstart": m.QuickstartCmd,
	}
}

func (m *Quickstart) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"quickstart": {
			Aliases: []string{"start"},
		},
	}
}

func (m *Quickstart) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

func (m *Quickstart) StartCmd(msg *goroku.Message) error {
	return m.showQuickstart(msg)
}

func (m *Quickstart) QuickstartCmd(msg *goroku.Message) error {
	return m.showQuickstart(msg)
}

func (m *Quickstart) showQuickstart(msg *goroku.Message) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		_ = msg.Delete()
		return m.sendMenu(msg.ChatID)
	}

	msg.Text = m.getWelcomeText()
	if msg.Client != nil {
		_, _ = msg.Client.EditMessage(msg.ChatID, msg.ID, msg.Text)
	}
	return nil
}

func (m *Quickstart) HandleBotPM(msg *tgbotapi.Message) {
	if msg == nil {
		return
	}

	if msg.Text == "/start" && msg.From != nil && msg.From.ID == m.client.TGID {
		_ = m.sendMenu(msg.Chat.ID)
	}
}

func (m *Quickstart) sendMenu(chatID int64) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := m.getWelcomeText()
	markup := m.generateWelcomeMarkup(im)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = markup
	msg.LinkPreviewOptions = tgbotapi.LinkPreviewOptions{IsDisabled: true}

	_, err := im.GetBotAPI().Send(msg)
	return err
}

func (m *Quickstart) editMenu(c inline.CallbackQuery) error {
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if !ok || im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := m.getWelcomeText()
	markup := m.generateWelcomeMarkup(im)

	return c.Edit(text, markup)
}

func (m *Quickstart) getWelcomeText() string {
	platform := "Goroku"
	me, err := m.client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}

	baseText := m.getTrans("base", "<tg-emoji emoji-id=5463379725441341739>🪐</tg-emoji> <b>Hello.</b> Your <b>{}</b> userbot is now installed.\n\n<tg-emoji emoji-id=5134202243486057363>💫</tg-emoji> <b>Need help?</b> Join <a href=\"https://t.me/goroku_talks\">our support chat</a>. We help <b>everyone</b>.\n\n<tg-emoji emoji-id=4940480187436369099>💁‍♀️</tg-emoji> <b>Quick Guide:</b>\n\n<tg-emoji emoji-id=5456197350416486261>1️⃣</tg-emoji> <b>Write</b> <code>.help</code> <b>to see the list of modules</b>\n<tg-emoji emoji-id=5456261689026581678>2️⃣</tg-emoji> <b>Write</b> <code>.help &lt;Module name/command&gt;</code> <b>to see the description of the module</b>\n<tg-emoji emoji-id=5458366235886522404>3️⃣</tg-emoji> <b>Write</b> <code>.dlmod &lt;link&gt;</code> <b>to load a module from a link</b>\n<tg-emoji emoji-id=5456207331920483861>4️⃣</tg-emoji> <b>Write</b> <code>.loadmod</code> <b>in response to a file to load a module from it</b>\n<tg-emoji emoji-id=5456185418997340146>5️⃣</tg-emoji> <b>Write</b> <code>.unloadmod &lt;Module name&gt;</code> <b>to unload a module</b>\n\n<tg-emoji emoji-id=5456178297941561360>💡</tg-emoji> <b>Goroku supports modules from Hikka, Friendly-Telegram, and GeekTG, as well as its own.</b>\n")
	text := strings.ReplaceAll(baseText, "{}", platform)

	if os.Getenv("LAVHOST") != "" {
		lavhostText := m.getTrans("lavhost", "✌️ <b>Your userbot is installed on lavHost</b>. Make sure to join @lavhost for important notifications and updates. All questions regarding the platform should be asked in @lavhostchat.")
		text += "\n" + lavhostText
	}
	return text
}

func (m *Quickstart) generateWelcomeMarkup(im *inline.InlineManager) tgbotapi.InlineKeyboardMarkup {
	var buttons [][]inline.Button

	buttons = append(buttons, []inline.Button{
		{
			Text: m.getTrans("btn_support", "Support chat"),
			URL:  "https://t.me/goroku_talks",
		},
	})

	var langs []string
	for k := range goroku.SupportedLanguages {
		langs = append(langs, k)
	}
	for k := range goroku.MemeLanguages {
		langs = append(langs, k)
	}
	sort.Strings(langs)

	var langBtns []inline.Button
	for _, lang := range langs {
		l := lang
		title := goroku.SupportedLanguages[l]
		if title == "" {
			title = goroku.MemeLanguages[l]
		}
		langBtns = append(langBtns, inline.Button{
			Text: title,
			Data: "lang_" + l + "_" + genRandStr(4),
			Handler: func(c inline.CallbackQuery) error {
				m.db.Set("goroku.translations", "lang", l)
				m.translator.Init()
				m.db.Save()

				saveTrans := getTrans(m.translator, "Translations", "language_saved", "Language saved!")
				_ = c.Answer(saveTrans, false)
				return m.editMenu(c)
			},
		})
	}

	for i := 0; i < len(langBtns); i += 3 {
		end := i + 3
		if end > len(langBtns) {
			end = len(langBtns)
		}
		buttons = append(buttons, langBtns[i:end])
	}

	return im.GenerateMarkup(buttons)
}

func (m *Quickstart) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}
