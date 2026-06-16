package modules

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"

	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
)

// GorokuBackup handles database and module backups.
type GorokuBackup struct {
	client       *goroku.CustomTelegramClient
	db           *goroku.Database
	translator   *goroku.Translator
	backupPeriod time.Duration
	lastBackup   time.Time
	stopBackup   chan struct{}
	stopOnce     sync.Once
}

type dummyMessage struct {
	chatID int64
	id     int64
}

func (d *dummyMessage) GetChatID() int64 { return d.chatID }
func (d *dummyMessage) GetID() int64     { return d.id }

// ── Module interface ──────────────────────────────────────────────────────────

func (m *GorokuBackup) Name() string { return "GorokuBackup" }

func (m *GorokuBackup) Strings() map[string]string {
	return map[string]string{
		"name": "Goroku Backup Module",
	}
}

func (m *GorokuBackup) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	m.stopBackup = make(chan struct{})

	// Load persisted backup period
	rawPeriod := m.db.Get("GorokuBackup", "period", nil)
	switch v := rawPeriod.(type) {
	case float64:
		if v > 0 {
			m.backupPeriod = time.Duration(v) * time.Second
		}
	case string:
		if v == "disabled" {
			m.backupPeriod = 0
		}
	}

	// Load persisted last-backup timestamp
	rawLast := m.db.Get("GorokuBackup", "last_backup", nil)
	if ts, ok := rawLast.(float64); ok && ts > 0 {
		m.lastBackup = time.Unix(int64(ts), 0)
	}

	return nil
}

func (m *GorokuBackup) OnDlmod() error { return nil }

func (m *GorokuBackup) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

// ClientReady starts the periodic backup goroutine and shows period setups if not set.
func (m *GorokuBackup) ClientReady() error {
	periodVal := m.db.Get("GorokuBackup", "period", nil)
	if periodVal == nil {
		im, ok := m.client.GorokuInline.(*inline.InlineManager)
		if ok && im != nil {
			go func() {
				// Wait for inline manager to be ready
				for i := 0; i < 30; i++ {
					if im.IsComplete() {
						break
					}
					time.Sleep(1 * time.Second)
				}
				if !im.IsComplete() {
					return
				}

				botAPI := im.GetBotAPI()
				if botAPI == nil {
					return
				}

				markup := [][]inline.Button{
					{
						m.makeBackupPeriodButton("🕰 1 h", 1),
						m.makeBackupPeriodButton("🕰 2 h", 2),
						m.makeBackupPeriodButton("🕰 4 h", 4),
					},
					{
						m.makeBackupPeriodButton("🕰 6 h", 6),
						m.makeBackupPeriodButton("🕰 8 h", 8),
						m.makeBackupPeriodButton("🕰 12 h", 12),
					},
					{
						m.makeBackupPeriodButton("🕰 24 h", 24),
						m.makeBackupPeriodButton("🕰 48 h", 48),
						m.makeBackupPeriodButton("🕰 168 h", 168),
					},
					{
						{
							Text: "🚫 Never",
							Data: fmt.Sprintf("bkp_period_0_%d", time.Now().UnixNano()),
							Handler: func(call inline.CallbackQuery) error {
								return m.handleSetBackupPeriodCallback(call, 0)
							},
						},
					},
				}

				periodText := m.getTrans("period", "⌚️ <b>The unit «ALPHA»</b> creates regular backups...")
				
				photo := tgbotapi.NewPhoto(m.client.TGID, tgbotapi.FileURL("https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/unit_alpha.png"))
				photo.Caption = periodText
				photo.ParseMode = tgbotapi.ModeHTML
				photo.ReplyMarkup = im.GenerateMarkup(markup)
				
				_, err := botAPI.Send(photo)
				if err != nil {
					log.Printf("Failed to send backup period msg via bot: %v\n", err)
				}
			}()
		}
	}

	go m.backupLoop()
	return nil
}

func (m *GorokuBackup) makeBackupPeriodButton(text string, hours int) inline.Button {
	return inline.Button{
		Text: text,
		Data: fmt.Sprintf("bkp_period_%d_%d", hours, time.Now().UnixNano()),
		Handler: func(call inline.CallbackQuery) error {
			return m.handleSetBackupPeriodCallback(call, hours)
		},
	}
}

func (m *GorokuBackup) handleSetBackupPeriodCallback(call inline.CallbackQuery, hours int) error {
	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}

	if hours == 0 {
		m.db.Set("GorokuBackup", "period", "disabled")
		m.backupPeriod = 0

		neverTrans := m.getTrans("never_bot", "✅ I will not make automatic backups. Can be cancelled using {prefix}set_backup_period")
		neverMsg := strings.ReplaceAll(neverTrans, "{prefix}", prefix)

		_ = call.Answer(neverMsg, true)
		_ = closeForm(call)
		return nil
	}

	periodSecs := hours * 3600
	m.db.Set("GorokuBackup", "period", float64(periodSecs))
	m.db.Set("GorokuBackup", "last_backup", float64(time.Now().Unix()))
	m.backupPeriod = time.Duration(periodSecs) * time.Second
	m.lastBackup = time.Now()

	savedTrans := m.getTrans("saved_bot", "✅ The periodicity is saved! It can be changed with {prefix}set_backup_period")
	savedMsg := strings.ReplaceAll(savedTrans, "{prefix}", prefix)

	_ = call.Answer(savedMsg, true)
	_ = closeForm(call)
	return nil
}

// OnUnload stops the background backup goroutine.
func (m *GorokuBackup) OnUnload() error {
	m.stopOnce.Do(func() {
		close(m.stopBackup)
	})
	return nil
}

func (m *GorokuBackup) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"backupdb":          m.BackupDBCmd,
		"restoredb":         m.RestoreDBCmd,
		"backupmods":        m.BackupModsCmd,
		"restoremods":       m.RestoreModsCmd,
		"backupall":         m.BackupAllCmd,
		"restoreall":        m.RestoreAllCmd,
		"set_backup_period": m.SetBackupPeriodCmd,
	}
}

func (m *GorokuBackup) CommandMetas() map[string]goroku.CommandMeta {
	return map[string]goroku.CommandMeta{
		"backupall": {
			Aliases: []string{"backup"},
		},
		"set_backup_period": {
			Aliases: []string{"setbackupperiod"},
		},
	}
}

func (m *GorokuBackup) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *GorokuBackup) getBackupTopicID() int32 {
	val := utils.GetTopicID(m.db, "Backups")
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		return int32(v)
	case int32:
		return v
	case int64:
		return int32(v)
	case float64:
		return int32(v)
	}
	return 0
}


func (m *GorokuBackup) handleConvertCallback(call inline.CallbackQuery, ans string, fileContent string) error {
	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}

	if ans == "y" {
		convertingText := m.getTrans("converting_db", "🔄 Converting...")
		_ = call.Edit(convertingText, tgbotapi.InlineKeyboardMarkup{})

		re := regexp.MustCompile(`"(hikka\.)(\S+":)`)
		converted := re.ReplaceAllString(fileContent, `"goroku.${2}`)

		filename := fmt.Sprintf("db-converted-%s.json", time.Now().Format("02-01-2006-15-04"))

		captionTrans := m.getTrans("backup_caption", "")
		caption := strings.ReplaceAll(captionTrans, "{prefix}", utils.EscapeHTML(prefix))

		nr := &namedReader{r: bytes.NewReader([]byte(converted)), name: filename}

		_, err := m.client.SendFile(call.ChatID, nr, caption)
		if err != nil {
			log.Printf("Convert send file error: %v", err)
		}
		_ = closeForm(call)
		return nil
	}

	adviceText := m.getTrans("advice_converting", "You can manually replace...")
	markup := [][]inline.Button{
		{
			{
				Text: "🔻 Close",
				Data: fmt.Sprintf("bkp_close_%d", time.Now().UnixNano()),
				Handler: func(call inline.CallbackQuery) error {
					return closeForm(call)
				},
			},
		},
	}
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil {
		_ = call.Edit(adviceText, im.GenerateMarkup(markup))
	}
	return nil
}

// ── Commands ──────────────────────────────────────────────────────────────────

// BackupDBCmd sends a JSON snapshot of the database.
func (m *GorokuBackup) BackupDBCmd(msg *goroku.Message) error {
	jsonBytes, err := m.buildDBJSON()
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to create DB backup: %v", err))
	}

	filename := fmt.Sprintf("db-backup-%s.json", time.Now().Format("02-01-2006-15-04"))
	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}
	captionTrans := m.getTrans("backup_caption", "")
	caption := strings.ReplaceAll(captionTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(jsonBytes), name: filename}

	contentChannelID := utils.WaitForContentChannel(m.db, 3)
	topicID := m.getBackupTopicID()

	if topicID == 0 {
		_, err = m.client.SendFile(msg.ChatID, nr, caption)
		if err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
		}
		return nil
	}

	res, err := m.client.SendFileWithOptions(
		int64(-1000000000000-contentChannelID),
		nr,
		caption,
		goroku.WithReplyTo(int64(topicID)),
	)
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
	}

	msgID := goroku.GetSentMessageID(res)
	link := fmt.Sprintf("https://t.me/c/%d/%d/%d", cleanChannelIDForLink(contentChannelID), topicID, msgID)

	sentTrans := m.getTrans("backup_sent", "")
	sentMsg := formatTrans(sentTrans, link)

	return msg.Answer(sentMsg)
}

// RestoreDBCmd restores the database from a replied backup JSON file.
func (m *GorokuBackup) RestoreDBCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(reply.Media, &buf)
	if err != nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	fileContent := string(buf.Bytes())

	reHikka := regexp.MustCompile(`"(hikka\.)(\S+":)`)
	if reHikka.MatchString(fileContent) {
		im, ok := m.client.GorokuInline.(*inline.InlineManager)
		if ok && im != nil && im.IsComplete() {
			markup := [][]inline.Button{
				{
					{
						Text: "❌",
						Data: fmt.Sprintf("bkp_conv_n_%d", time.Now().UnixNano()),
						Handler: func(call inline.CallbackQuery) error {
							return m.handleConvertCallback(call, "n", fileContent)
						},
					},
					{
						Text: "✅",
						Data: fmt.Sprintf("bkp_conv_y_%d", time.Now().UnixNano()),
						Handler: func(call inline.CallbackQuery) error {
							return m.handleConvertCallback(call, "y", fileContent)
						},
					},
				},
			}
			warningTrans := m.getTrans("db_warning", "❗️ Hikka backup detected...")
			_, err = im.Form(warningTrans, msg, markup)
			return err
		}
	}

	var backupData map[string]map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &backupData)
	if err != nil {
		prefix := "."
		if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
			prefix = val
		}
		probZipTrans := m.getTrans("probably_zip", "")
		probZipMsg := strings.ReplaceAll(probZipTrans, "{}", prefix)
		return msg.Answer(probZipMsg)
	}

	if inlineVal, ok := backupData["goroku.inline"]; ok {
		delete(inlineVal, "bot_token")
	}

	m.db.Reset(backupData)

	dbRestoredTrans := m.getTrans("db_restored", "Database updated, restarting...")
	_ = msg.Answer(dbRestoredTrans)

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}

// BackupModsCmd sends a zip archive of the modules.
func (m *GorokuBackup) BackupModsCmd(msg *goroku.Message) error {
	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	loadedModsBytes, err := json.MarshalIndent(loadedMods, "", "  ")
	if err == nil {
		_ = addFileToZip(zw, "db_mods.json", loadedModsBytes)
	}

	modsDir := filepath.Join(goroku.BasePath, "goroku", "modules")
	modsCount := len(loadedMods)
	for modName := range loadedMods {
		fileName := modName + ".go"
		path := filepath.Join(modsDir, fileName)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			content, readErr := os.ReadFile(path)
			if readErr == nil {
				_ = addFileToZip(zw, fileName, content)
			}
		}
	}

	if err := zw.Close(); err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to close modules backup zip: %v", err))
	}

	filename := fmt.Sprintf("mods-%s.zip", time.Now().Format("02-01-2006-15-04"))

	captionTrans := m.getTrans("modules_backup", "")
	caption := formatTrans(captionTrans, strconv.Itoa(modsCount), prefix)

	nr := &namedReader{r: bytes.NewReader(buf.Bytes()), name: filename}

	contentChannelID := utils.WaitForContentChannel(m.db, 3)
	topicID := m.getBackupTopicID()

	if topicID == 0 {
		_, err = m.client.SendFile(msg.ChatID, nr, caption)
		if err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
		}
		return nil
	}

	res, err := m.client.SendFileWithOptions(
		int64(-1000000000000-contentChannelID),
		nr,
		caption,
		goroku.WithReplyTo(int64(topicID)),
	)
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
	}

	msgID := goroku.GetSentMessageID(res)
	link := fmt.Sprintf("https://t.me/c/%d/%d/%d", cleanChannelIDForLink(contentChannelID), topicID, msgID)

	sentTrans := m.getTrans("backup_sent", "")
	sentMsg := formatTrans(sentTrans, link)

	return msg.Answer(sentMsg)
}

// RestoreModsCmd extracts and restores custom modules from a replied ZIP file.
func (m *GorokuBackup) RestoreModsCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(reply.Media, &buf)
	if err != nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		var dbMods map[string]string
		err = json.Unmarshal(buf.Bytes(), &dbMods)
		if err == nil {
			m.db.Set("Loader", "loaded_modules", dbMods)
			modsRestoredTrans := m.getTrans("mods_restored", "Modules restored, restarting")
			_ = msg.Answer(modsRestoredTrans)
			go func() {
				time.Sleep(1 * time.Second)
				goroku.Restart()
			}()
			return nil
		}

		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	modsDir := filepath.Join(goroku.BasePath, "goroku", "modules")
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if file.Name == "db_mods.json" {
			mrc, err := file.Open()
			if err == nil {
				mBytes, err := io.ReadAll(mrc)
				mrc.Close()
				if err == nil {
					var dbMods map[string]string
					if err := json.Unmarshal(mBytes, &dbMods); err == nil {
						m.db.Set("Loader", "loaded_modules", dbMods)
					}
				}
			}
		} else if strings.HasSuffix(file.Name, ".go") {
			mrc, err := file.Open()
			if err == nil {
				mBytes, err := io.ReadAll(mrc)
				mrc.Close()
				if err == nil {
					outPath := filepath.Join(modsDir, filepath.Base(file.Name))
					_ = os.WriteFile(outPath, mBytes, 0644)
				}
			}
		}
	}

	modsRestoredTrans := m.getTrans("mods_restored", "Modules restored, restarting")
	_ = msg.Answer(modsRestoredTrans)

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}

func (m *GorokuBackup) restoreAllFromZip(data []byte) error {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	modsDir := filepath.Join(goroku.BasePath, "goroku", "modules")
	var dbRestored bool

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if file.Name == "db.json" {
			rc, err := file.Open()
			if err != nil {
				return err
			}
			dbBytes, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}

			var backupData map[string]map[string]interface{}
			err = json.Unmarshal(dbBytes, &backupData)
			if err != nil {
				return err
			}

			if inline, ok := backupData["goroku.inline"]; ok {
				delete(inline, "bot_token")
			}
			m.db.Reset(backupData)
			dbRestored = true
		} else if file.Name == "mods.zip" {
			rc, err := file.Open()
			if err != nil {
				return err
			}
			modsZipBytes, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}

			mzReader, err := zip.NewReader(bytes.NewReader(modsZipBytes), int64(len(modsZipBytes)))
			if err == nil {
				for _, mzFile := range mzReader.File {
					if mzFile.FileInfo().IsDir() {
						continue
					}
					if mzFile.Name == "db_mods.json" {
						mrc, err := mzFile.Open()
						if err == nil {
							mBytes, err := io.ReadAll(mrc)
							mrc.Close()
							if err == nil {
								var dbMods map[string]string
								if err := json.Unmarshal(mBytes, &dbMods); err == nil {
									m.db.Set("Loader", "loaded_modules", dbMods)
								}
							}
						}
					} else if strings.HasSuffix(mzFile.Name, ".go") {
						mrc, err := mzFile.Open()
						if err == nil {
							mBytes, err := io.ReadAll(mrc)
							mrc.Close()
							if err == nil {
								outPath := filepath.Join(modsDir, filepath.Base(mzFile.Name))
								_ = os.WriteFile(outPath, mBytes, 0644)
							}
						}
					}
				}
			}
		}
	}

	if !dbRestored {
		return fmt.Errorf("db.json not found in archive")
	}

	return nil
}

// RestoreAllCmd restores both database and custom modules from a replied ZIP file.
func (m *GorokuBackup) RestoreAllCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(reply.Media, &buf)
	if err != nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	err = m.restoreAllFromZip(buf.Bytes())
	if err != nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	allRestoredTrans := m.getTrans("all_restored", "Your full backup has been restored, restarting...")
	_ = msg.Answer(allRestoredTrans)

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}

// BackupAllCmd sends a zip archive containing db.json + mods/*.go files.
func (m *GorokuBackup) BackupAllCmd(msg *goroku.Message) error {
	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}

	archiveBytes, err := m.buildArchive()
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to create backup archive: %v", err))
	}

	filename := fmt.Sprintf("goroku-%s.backup", time.Now().Format("02-01-2006-15-04"))

	infoTrans := m.getTrans("backupall_info", "")
	caption := strings.ReplaceAll(infoTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(archiveBytes), name: filename}

	contentChannelID := utils.WaitForContentChannel(m.db, 3)
	topicID := m.getBackupTopicID()

	// 1. Send file via userbot to the forum topic (or PM if topicID == 0)
	var res interface{}
	if topicID == 0 {
		res, err = m.client.SendFile(msg.ChatID, nr, caption)
	} else {
		res, err = m.client.SendFileWithOptions(
			int64(-1000000000000-contentChannelID),
			nr,
			caption,
			goroku.WithReplyTo(int64(topicID)),
		)
	}
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to send backup file: %v", err))
	}

	msgID := goroku.GetSentMessageID(res)
	if msgID == 0 {
		return msg.Answer("❌ Failed to get sent message ID")
	}

	// 2. If inline bot is ready, send a Form with "Restore this" button that references the sent message ID
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		markup := [][]inline.Button{
			{
				{
					Text: "↪️ Restore this",
					Data: fmt.Sprintf("bkp_rst_%d_%d", msgID, time.Now().UnixNano()),
					Handler: func(call inline.CallbackQuery) error {
						return m.handleRestoreFromMessageCallback(call, int64(msgID))
					},
				},
			},
		}

		targetChat := msg.ChatID
		if topicID != 0 {
			targetChat = int64(-1000000000000 - contentChannelID)
		}

		dummy := &dummyMessage{
			chatID: targetChat,
			id:     int64(topicID),
		}

		formText := m.getTrans("backupall_sent", "")
		link := fmt.Sprintf("https://t.me/c/%d/%d/%d", cleanChannelIDForLink(contentChannelID), topicID, msgID)
		formTextFormatted := formatTrans(formText, link)

		var formTarget interface{} = dummy
		if topicID == 0 {
			formTarget = msg
		}

		_, err = im.Form(formTextFormatted, formTarget, markup)
		if err != nil {
			log.Printf("Failed to send inline restore form: %v", err)
		}

		if topicID != 0 {
			return msg.Answer(formTextFormatted)
		}
		return nil
	}

	// If inline is not complete, just print the text link to the sent file
	link := fmt.Sprintf("https://t.me/c/%d/%d/%d", cleanChannelIDForLink(contentChannelID), topicID, msgID)
	sentTrans := m.getTrans("backupall_sent", "")
	sentMsg := formatTrans(sentTrans, link)
	return msg.Answer(sentMsg)
}

func (m *GorokuBackup) handleRestoreFromMessageCallback(call inline.CallbackQuery, targetMsgID int64) error {
	markup := [][]inline.Button{
		{
			{
				Text: "✅ Yes",
				Data: fmt.Sprintf("bkp_rst_y_%d_%d", targetMsgID, time.Now().UnixNano()),
				Handler: func(call inline.CallbackQuery) error {
					return m.handleRestoreExecuteFromMessageCallback(call, targetMsgID)
				},
			},
		},
	}
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil {
		_ = call.Edit("❓ <b>Are you sure?</b>", im.GenerateMarkup(markup))
	}
	return nil
}

func (m *GorokuBackup) handleRestoreExecuteFromMessageCallback(call inline.CallbackQuery, targetMsgID int64) error {
	msg, err := m.client.GetMessage(call.ChatID, targetMsgID)
	if err != nil || msg == nil || msg.Media == nil {
		alertText := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		_ = call.Answer(alertText, true)
		return nil
	}

	var buf bytes.Buffer
	err = m.client.DownloadMedia(msg.Media, &buf)
	if err != nil {
		_ = call.Answer(fmt.Sprintf("Failed to download media: %v", err), true)
		return nil
	}

	err = m.restoreAllFromZip(buf.Bytes())
	if err != nil {
		alertText := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		_ = call.Answer(alertText, true)
		return nil
	}

	restoredText := m.getTrans("all_restored_bot", "Your full backup has been restored, restarting...")
	_ = call.Answer(restoredText, true)
	_ = closeForm(call)

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

	return nil
}

// SetBackupPeriodCmd parses an integer number of hours and stores it.
func (m *GorokuBackup) SetBackupPeriodCmd(msg *goroku.Message) error {
	raw := strings.TrimSpace(utils.GetArgsRaw(msg.Text))

	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}

	hours, err := strconv.Atoi(raw)
	if err != nil || hours < 0 || hours >= 200 {
		invalidTrans := m.getTrans("invalid_args", "🚫 <b>Please specify the correct frequency in hours, or `0` to disable</b>")
		return msg.Answer(invalidTrans)
	}

	if hours == 0 {
		m.db.Set("GorokuBackup", "period", "disabled")
		m.backupPeriod = 0

		neverTrans := m.getTrans("never", "✅ I will not make automatic backups. Can be cancelled using {prefix}set_backup_period")
		neverMsg := "<b>" + strings.ReplaceAll(neverTrans, "{prefix}", prefix) + "</b>"
		return msg.Answer(neverMsg)
	}

	periodSecs := hours * 3600
	m.db.Set("GorokuBackup", "period", float64(periodSecs))
	m.db.Set("GorokuBackup", "last_backup", float64(time.Now().Unix()))
	m.backupPeriod = time.Duration(periodSecs) * time.Second
	m.lastBackup = time.Now()

	savedTrans := m.getTrans("saved", "✅ The periodicity is saved! It can be changed with {prefix}set_backup_period")
	savedMsg := "<b>" + strings.ReplaceAll(savedTrans, "{prefix}", prefix) + "</b>"
	return msg.Answer(savedMsg)
}

// ── Background goroutine ──────────────────────────────────────────────────────

func (m *GorokuBackup) backupLoop() {
	for {
		period := m.backupPeriod
		if period == 0 {
			select {
			case <-m.stopBackup:
				return
			case <-time.After(10 * time.Second):
				// Reload from DB
				rawPeriod := m.db.Get("GorokuBackup", "period", nil)
				switch v := rawPeriod.(type) {
				case float64:
					if v > 0 {
						m.backupPeriod = time.Duration(v) * time.Second
					}
				case string:
					if v == "disabled" {
						m.backupPeriod = 0
					}
				}
				continue
			}
		}

		due := m.lastBackup.Add(period)
		sleepFor := time.Until(due)
		if sleepFor < 0 {
			sleepFor = 0
		}

		select {
		case <-m.stopBackup:
			return
		case <-time.After(sleepFor):
		}

		if m.backupPeriod > 0 {
			if err := m.sendPeriodicBackup(); err != nil {
				log.Printf("GorokuBackup periodic backup failed: %v", err)
				select {
				case <-m.stopBackup:
					return
				case <-time.After(60 * time.Second):
				}
				continue
			}
			m.lastBackup = time.Now()
			m.db.Set("GorokuBackup", "last_backup", float64(m.lastBackup.Unix()))
		}
	}
}

func (m *GorokuBackup) sendPeriodicBackup() error {
	archiveBytes, err := m.buildArchive()
	if err != nil {
		return fmt.Errorf("build archive: %w", err)
	}

	filename := fmt.Sprintf("backup-%s.backup", time.Now().Format("02-01-2006-15-04"))

	prefix := "."
	if val, ok := m.db.Get("goroku.main", "command_prefix", ".").(string); ok && val != "" {
		prefix = val
	}
	infoTrans := m.getTrans("backupall_info", "")
	caption := strings.ReplaceAll(infoTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(archiveBytes), name: filename}

	contentChannelID := utils.WaitForContentChannel(m.db, 3)
	topicID := m.getBackupTopicID()

	// Send document via userbot
	var res interface{}
	if topicID == 0 {
		res, err = m.client.SendFile(m.client.TGID, nr, caption)
	} else {
		res, err = m.client.SendFileWithOptions(
			int64(-1000000000000-contentChannelID),
			nr,
			caption,
			goroku.WithReplyTo(int64(topicID)),
		)
	}
	if err != nil {
		return err
	}

	msgID := goroku.GetSentMessageID(res)
	if msgID == 0 {
		return nil
	}

	// Send Form with button if inline is ready
	im, ok := m.client.GorokuInline.(*inline.InlineManager)
	if ok && im != nil && im.IsComplete() {
		markup := [][]inline.Button{
			{
				{
					Text: "↪️ Restore this",
					Data: fmt.Sprintf("bkp_rst_%d_%d", msgID, time.Now().UnixNano()),
					Handler: func(call inline.CallbackQuery) error {
						return m.handleRestoreFromMessageCallback(call, int64(msgID))
					},
				},
			},
		}

		targetChat := m.client.TGID
		if topicID != 0 {
			targetChat = int64(-1000000000000 - contentChannelID)
		}

		dummy := &dummyMessage{
			chatID: targetChat,
			id:     int64(topicID),
		}

		formText := m.getTrans("backupall_sent", "")
		link := fmt.Sprintf("https://t.me/c/%d/%d/%d", cleanChannelIDForLink(contentChannelID), topicID, msgID)
		formTextFormatted := formatTrans(formText, link)

		_, _ = im.Form(formTextFormatted, dummy, markup)
	}

	return nil
}

// buildDBJSON serialises the full database.
func (m *GorokuBackup) buildDBJSON() ([]byte, error) {
	data := m.db.GetAll()
	return json.MarshalIndent(data, "", "  ")
}

// buildArchive creates a zip archive containing:
//   - db.json     – full database snapshot
//   - mods.zip    – zip containing loaded modules + db_mods.json
func (m *GorokuBackup) buildArchive() ([]byte, error) {
	dbJSON, err := m.buildDBJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal db: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Write db.json
	if err := addFileToZip(zw, "db.json", dbJSON); err != nil {
		return nil, err
	}

	// Build mods.zip
	var modsBuf bytes.Buffer
	mzw := zip.NewWriter(&modsBuf)

	loadedMods := make(map[string]string)
	val := m.db.Get("Loader", "loaded_modules", nil)
	if val != nil {
		if bytesData, err := json.Marshal(val); err == nil {
			json.Unmarshal(bytesData, &loadedMods) //nolint:errcheck
		}
	}

	loadedModsBytes, err := json.MarshalIndent(loadedMods, "", "  ")
	if err == nil {
		_ = addFileToZip(mzw, "db_mods.json", loadedModsBytes)
	}

	modsDir := filepath.Join(goroku.BasePath, "goroku", "modules")
	for modName := range loadedMods {
		fileName := modName + ".go"
		path := filepath.Join(modsDir, fileName)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			content, readErr := os.ReadFile(path)
			if readErr == nil {
				_ = addFileToZip(mzw, fileName, content)
			}
		}
	}
	_ = mzw.Close()

	// Write mods.zip
	if err := addFileToZip(zw, "mods.zip", modsBuf.Bytes()); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addFileToZip(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// namedReader wraps a bytes.Reader and exposes a Name() method.
type namedReader struct {
	r    *bytes.Reader
	name string
}

func (nr *namedReader) Read(p []byte) (int, error) { return nr.r.Read(p) }
func (nr *namedReader) Name() string               { return nr.name }

func cleanChannelIDForLink(id int64) int64 {
	if id < 0 {
		id = -id
	}
	s := strconv.FormatInt(id, 10)
	if strings.HasPrefix(s, "100") && len(s) > 3 {
		if val, err := strconv.ParseInt(s[3:], 10, 64); err == nil {
			return val
		}
	}
	return id
}
