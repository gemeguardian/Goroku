package modules

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/inlineiface"
	"goroku/goroku/utils"
)

type quickstart struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
}

func StartQuickstart(ctx context.Context, client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m := &quickstart{
		client:     client,
		db:         db,
		translator: goroku.NewTranslator(client, db),
	}
	m.translator.Init()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				L().Error("Quickstart ClientReady panic recovered", zap.Any("panic", r))
			}
		}()
		if err := ctx.Err(); err != nil {
			return
		}

		var contentChannel any
		var finalCid int64
		cid := m.db.GetInt64("goroku.forums", "channel_id", 0)
		if cid != 0 {
			if cid > 0 {
				cid = goroku.TelegramChannelChatID(cid)
				if err := m.db.SetInt64("goroku.forums", "channel_id", cid); err != nil {
					L().Error("background database write failed", zap.String("operation", "set"), zap.String("owner", "goroku.forums"), zap.String("key", "channel_id"), zap.Error(err))
					return
				}
			}
			peer, err := m.client.ResolvePeer(cid)
			if err == nil {
				contentChannel = peer
				finalCid = cid
			}
		}

		if contentChannel == nil {
			peer, err := m.client.FindChannelByTitle("goroku-userbot")
			if err == nil {
				var cid int64
				if ch, ok := peer.(*tg.InputPeerChannel); ok {
					cid = goroku.TelegramChannelChatID(ch.ChannelID)
				}
				if err := m.db.SetInt64("goroku.forums", "channel_id", cid); err != nil {
					L().Error("background database write failed", zap.String("operation", "set"), zap.String("owner", "goroku.forums"), zap.String("key", "channel_id"), zap.Error(err))
					return
				}
				contentChannel = peer
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
				var cid int64
				if ch, ok := peer.(*tg.InputPeerChannel); ok {
					cid = goroku.TelegramChannelChatID(ch.ChannelID)
				}
				if err := m.db.SetInt64("goroku.forums", "channel_id", cid); err != nil {
					L().Error("background database write failed", zap.String("operation", "set"), zap.String("owner", "goroku.forums"), zap.String("key", "channel_id"), zap.Error(err))
					return
				}
				contentChannel = peer
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
				L().Warn("Quickstart: failed to create forum topic", zap.Any("topic", topic.Title), zap.Error(err))
			}
		}

		_ = finalCid

		// Welcome message with language selector
		if !m.db.GetBool("Quickstart", "no_msg", false) {
			im := m.client.GorokuInline
			if im != nil && im.IsComplete() {
				if err := m.sendMenu(m.client.TGIDValue()); err != nil {
					return
				}
				if err := m.db.SetBool("Quickstart", "no_msg", true); err != nil {
					L().Error("background database write failed", zap.String("operation", "set"), zap.String("owner", "Quickstart"), zap.String("key", "no_msg"), zap.Error(err))
				}
			}
		}
	}()

	return nil
}

func (m *quickstart) sendMenu(chatID int64) error {
	im := m.client.GorokuInline
	if im == nil {
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

func (m *quickstart) editMenu(c inline.CallbackQuery) error {
	im := m.client.GorokuInline
	if im == nil {
		return fmt.Errorf("inline manager not ready")
	}

	text := m.getWelcomeText()
	markup := m.generateWelcomeMarkup(im)

	return c.Edit(text, markup)
}

func (m *quickstart) getWelcomeText() string {
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

func (m *quickstart) generateWelcomeMarkup(im inlineiface.InlineManager) tgbotapi.InlineKeyboardMarkup {
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
				if err := m.db.SetString("goroku.translations", "lang", l); err != nil {
					return fmt.Errorf("save language: %w", err)
				}
				m.translator.Init()

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

func (m *quickstart) getTrans(key, def string) string {
	return getTrans(m.translator, "Quickstart", key, def)
}
