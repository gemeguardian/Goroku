package modules

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"go.uber.org/zap"

	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/logger"
	"goroku/goroku/utils"
)

func L() *zap.Logger { return logger.L() }

// contentChannelOrFallback resolves the content channel used to file backups
// into a forum topic. A missing channel is not fatal: it collapses to the
// topicID == 0 case, which every caller already handles by delivering to the
// originating chat instead. Returning here rather than blocking is what keeps a
// never-created channel from pinning a dispatcher slot.
func (m *GorokuBackup) contentChannelOrFallback(ctx context.Context, topicID int32) (int64, int32) {
	channelID, err := utils.WaitForContentChannel(ctx, m.db, 3*time.Second, 0)
	if err != nil {
		L().Warn("content channel unavailable; delivering backup to the originating chat instead",
			zap.Error(err))
		return 0, 0
	}
	return channelID, topicID
}

// GorokuBackup handles database and module backups.
type GorokuBackup struct {
	client       *goroku.CustomTelegramClient
	db           *goroku.Database
	translator   *goroku.Translator
	backupPeriod time.Duration
	lastBackup   time.Time
	stopBackup   chan struct{}
	stopOnce     sync.Once
	// Narrow seams used by backup and restore tests.
	restoreApplyFile        func(string, string) error
	restoreDBReset          func(map[string]map[string]any) error
	compileModuleValidation func(string, []byte) error
	scheduleRestart         func()
}

type dummyMessage struct {
	chatID int64
	id     int64
}

type forwardRestoreCommitWarning struct{ err error }

func (e *forwardRestoreCommitWarning) Error() string { return e.err.Error() }
func (e *forwardRestoreCommitWarning) Unwrap() error { return e.err }

func isForwardRestoreCommitWarning(err error) bool {
	var warning *forwardRestoreCommitWarning
	return errors.As(err, &warning)
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

	// Recover FS from an interrupted restore before any scheduled backup work.
	// See restore_journal.go for residual crash-window documentation.
	if err := recoverIncompleteModuleRestore(m.db); err != nil {
		return fmt.Errorf("recover incomplete module restore: %w", err)
	}

	if err := m.reloadBackupPeriod(); err != nil {
		return fmt.Errorf("load backup period: %w", err)
	}

	rawLastBackup, err := m.db.Get("GorokuBackup", "last_backup", nil)
	if err != nil {
		return fmt.Errorf("load last backup timestamp: %w", err)
	}
	if ts := backupTimestamp(rawLastBackup); ts > 0 {
		m.lastBackup = time.Unix(ts, 0)
	}

	return nil
}

func (m *GorokuBackup) OnDlmod() error { return nil }

func (m *GorokuBackup) getTrans(key, def string) string {
	return getTrans(m.translator, m.Name(), key, def)
}

func scheduleBackupRestart() {
	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
}

func (m *GorokuBackup) scheduleBackupRestart() {
	if m.scheduleRestart != nil {
		m.scheduleRestart()
		return
	}
	scheduleBackupRestart()
}

func (m *GorokuBackup) loadedModulesMapChecked() (map[string]string, error) {
	loadedMods := make(map[string]string)
	val, err := m.db.Get("Loader", "loaded_modules", nil)
	if err != nil {
		return nil, fmt.Errorf("read module manifest: %w", err)
	}
	if val != nil {
		bytesData, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("marshal module manifest: %w", err)
		}
		loadedMods, err = parseModuleManifest(bytesData)
		if err != nil {
			return nil, fmt.Errorf("invalid module manifest: %w", err)
		}
	}
	return loadedMods, nil
}

// ClientReady starts the periodic backup goroutine and shows period setups if not set.
func (m *GorokuBackup) ClientReady() error {
	periodVal, err := m.db.Get("GorokuBackup", "period", nil)
	if err != nil {
		return fmt.Errorf("read backup period during client-ready: %w", err)
	}
	if periodVal == nil {
		im := m.client.GorokuInline
		if im != nil {
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
					L().Warn("Failed to send backup period msg via bot", zap.Error(err))
				}
			}()
		}
	}

	go m.backupLoop()
	return nil
}

func backupTimestamp(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		if v >= math.MinInt64 && v <= math.MaxInt64 {
			return int64(v)
		}
	}
	return 0
}

func (m *GorokuBackup) reloadBackupPeriod() error {
	rawPeriod, err := m.db.Get("GorokuBackup", "period", nil)
	if err != nil {
		return err
	}
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
	return nil
}

func (m *GorokuBackup) commandPrefix() string {
	// Prefix is presentation-only. Keep the typed getter's compatibility
	// fallback so a lifecycle read failure cannot hide the primary operation.
	return m.db.GetString("goroku.main", "command_prefix", ".")
}

func (m *GorokuBackup) completeRestore(err error, notify func(error)) error {
	if err != nil {
		if !isForwardRestoreCommitWarning(err) {
			return err
		}
	}
	notify(err)
	m.scheduleBackupRestart()
	return err
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
	prefix := m.commandPrefix()

	if hours == 0 {
		if err := m.setBackupPeriod(hours, time.Now()); err != nil {
			return fmt.Errorf("disable backup period: %w", err)
		}

		neverTrans := m.getTrans("never_bot", "✅ I will not make automatic backups. Can be cancelled using {prefix}set_backup_period")
		neverMsg := strings.ReplaceAll(neverTrans, "{prefix}", prefix)

		_ = call.Answer(neverMsg, true)
		_ = closeForm(call)
		return nil
	}

	if err := m.setBackupPeriod(hours, time.Now()); err != nil {
		return fmt.Errorf("save backup schedule: %w", err)
	}

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

const (
	maxRestoreFiles             = 256
	maxRestoreCompressedBytes   = 64 << 20
	maxRestoreUncompressedBytes = 64 << 20
	maxRestoreNestedBytes       = 32 << 20
	maxRestoreModules           = 128
	maxRestoreModuleNameBytes   = 128
	maxRestoreModuleURLBytes    = 2048
	maxRestoreEntryNameBytes    = 255
)

type limitedRestoreBuffer struct {
	bytes.Buffer
	remaining int64
}

func (w *limitedRestoreBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("backup exceeds %d compressed bytes", maxRestoreCompressedBytes)
	}
	w.remaining -= int64(len(p))
	return w.Buffer.Write(p)
}

func downloadRestoreMedia(download func(io.Writer) error) ([]byte, error) {
	w := &limitedRestoreBuffer{remaining: maxRestoreCompressedBytes}
	if err := download(w); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

type restoreLimits struct {
	files       int
	totalBytes  uint64
	nestedBytes uint64
}

func (l *restoreLimits) account(files []*zip.File, nested bool) error {
	for _, file := range files {
		l.files++
		if l.files > maxRestoreFiles {
			return fmt.Errorf("backup contains more than %d files", maxRestoreFiles)
		}
		if file.FileInfo().IsDir() {
			continue
		}

		size := file.UncompressedSize64
		if l.totalBytes > maxRestoreUncompressedBytes || size > maxRestoreUncompressedBytes-l.totalBytes {
			return fmt.Errorf("backup exceeds %d uncompressed bytes", maxRestoreUncompressedBytes)
		}
		l.totalBytes += size
		if nested {
			if l.nestedBytes > maxRestoreNestedBytes || size > maxRestoreNestedBytes-l.nestedBytes {
				return fmt.Errorf("nested backup content exceeds %d bytes", maxRestoreNestedBytes)
			}
			l.nestedBytes += size
		}
	}
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, int64(file.UncompressedSize64)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != file.UncompressedSize64 {
		return nil, fmt.Errorf("unexpected uncompressed size for %q", file.Name)
	}
	return data, nil
}

func parseRestoreDatabase(data []byte) (map[string]map[string]any, error) {
	var backupData map[string]map[string]any
	if err := json.Unmarshal(data, &backupData); err != nil {
		return nil, err
	}
	if len(backupData) == 0 {
		return nil, fmt.Errorf("database document must be a non-empty object")
	}
	for owner, values := range backupData {
		if owner == "" || values == nil {
			return nil, fmt.Errorf("invalid database owner %q", owner)
		}
	}
	return backupData, nil
}

func restoreDatabasePayload(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, nil
	}
	for _, file := range zr.File {
		if file.Name == "db.json" && file.Mode().IsRegular() {
			return readZipFile(file)
		}
	}
	return nil, fmt.Errorf("this ZIP does not contain db.json; use a database or full backup")
}

func restoreModulesPayload(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		var document map[string]json.RawMessage
		if json.Unmarshal(data, &document) == nil {
			for _, value := range document {
				trimmed := bytes.TrimSpace(value)
				if len(trimmed) > 0 && trimmed[0] == '{' {
					return nil, fmt.Errorf("this is a database-only backup; use .restoredb (it does not contain module source files)")
				}
			}
		}
		return data, nil
	}
	for _, file := range zr.File {
		if file.Name == "mods.zip" && file.Mode().IsRegular() {
			return readZipFile(file)
		}
	}
	return data, nil
}

func (m *GorokuBackup) restoreFailure(key, fallback string, err error) string {
	return formatTrans(m.getTrans(key, fallback), utils.EscapeHTML(err.Error()))
}

func (m *GorokuBackup) getBackupTopicID() int32 {
	val := utils.GetTopicID(m.db, "Backups")
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return 0
		}
		return int32(v)
	case int32:
		return v
	case int64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return 0
		}
		return int32(v)
	case float64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return 0
		}
		return int32(v)
	}
	return 0
}

func (m *GorokuBackup) handleConvertCallback(call inline.CallbackQuery, ans string, fileContent string) error {
	prefix := m.commandPrefix()

	if ans == "y" {
		convertingText := m.getTrans("converting_db", "🔄 Converting...")
		_ = call.Edit(convertingText, tgbotapi.InlineKeyboardMarkup{})

		re := regexp.MustCompile(`"(hikka\.)(\S+":)`)
		converted := re.ReplaceAllString(fileContent, `"goroku.${2}`)

		filename := fmt.Sprintf("db-converted-%s.json", time.Now().Format("02-01-2006-15-04"))

		captionTrans := m.getTrans("backup_caption", "")
		caption := strings.ReplaceAll(captionTrans, "{prefix}", utils.EscapeHTML(prefix))

		nr := &namedReader{r: bytes.NewReader([]byte(converted)), name: filename}

		_, err := m.client.SendFile(goroku.ChatRefID(call.ChatID), nr, caption)
		if err != nil {
			L().Warn("Convert send file error", zap.Error(err))
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
	im := m.client.GorokuInline
	if im != nil {
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
	prefix := m.commandPrefix()
	captionTrans := m.getTrans("backup_caption", "")
	caption := strings.ReplaceAll(captionTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(jsonBytes), name: filename}

	contentChannelID, topicID := m.contentChannelOrFallback(msg.Context(), m.getBackupTopicID())

	if topicID == 0 {
		_, err = m.client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption)
		if err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
		}
		return nil
	}

	res, err := m.client.SendFileWithOptions(goroku.ChatRefID(int64(-1000000000000-contentChannelID)),
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

	backupBytes, err := downloadRestoreMedia(func(w io.Writer) error {
		return m.client.DownloadMedia(reply.Media, w)
	})
	if err != nil {
		return msg.Answer(m.restoreFailure("restore_db_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Database restore failed:</b> <code>{}</code>", fmt.Errorf("could not download the replied file: %w", err)))
	}
	backupBytes, err = restoreDatabasePayload(backupBytes)
	if err != nil {
		return msg.Answer(m.restoreFailure("restore_db_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Database restore failed:</b> <code>{}</code>", err))
	}

	fileContent := string(backupBytes)

	reHikka := regexp.MustCompile(`"(hikka\.)(\S+":)`)
	if reHikka.MatchString(fileContent) {
		im := m.client.GorokuInline
		if im != nil && im.IsComplete() {
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

	restoreErr := m.restoreDatabaseFromData(backupBytes)
	if restoreErr != nil && !isForwardRestoreCommitWarning(restoreErr) {
		return msg.Answer(m.restoreFailure("restore_db_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Database restore failed:</b> <code>{}</code>", restoreErr))
	}

	return m.completeRestore(restoreErr, func(warning error) {
		dbRestoredTrans := m.getTrans("db_restored", "Database updated, restarting...")
		if warning != nil {
			dbRestoredTrans += fmt.Sprintf("\n\n⚠️ Database was restored, but durability could not be confirmed: %v", warning)
		}
		_ = msg.Answer(dbRestoredTrans)
	})
}

func (m *GorokuBackup) restoreDatabaseFromData(data []byte) error {
	backupData, err := parseRestoreDatabase(data)
	if err != nil {
		return err
	}
	m.preserveRestoreSecrets(backupData)
	return m.restoreDatabase(backupData)
}

func (m *GorokuBackup) restoreDatabase(backupData map[string]map[string]any) error {
	if len(backupData) == 0 {
		return fmt.Errorf("database document must be a non-empty object")
	}
	return withModuleTransaction(func() error {
		return m.applyRestore(backupData, nil)
	})
}

// BackupModsCmd sends a zip archive of the modules.
func (m *GorokuBackup) BackupModsCmd(msg *goroku.Message) error {
	prefix := m.commandPrefix()

	modsArchive, modsCount, err := m.buildModulesArchive()
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to create modules backup: %v", err))
	}

	filename := fmt.Sprintf("mods-%s.zip", time.Now().Format("02-01-2006-15-04"))

	captionTrans := m.getTrans("modules_backup", "")
	caption := formatTrans(captionTrans, strconv.Itoa(modsCount), prefix)

	nr := &namedReader{r: bytes.NewReader(modsArchive), name: filename}

	contentChannelID, topicID := m.contentChannelOrFallback(msg.Context(), m.getBackupTopicID())

	if topicID == 0 {
		_, err = m.client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption)
		if err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to send backup: %v", err))
		}
		return nil
	}

	res, err := m.client.SendFileWithOptions(goroku.ChatRefID(int64(-1000000000000-contentChannelID)),
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

	backupBytes, err := downloadRestoreMedia(func(w io.Writer) error {
		return m.client.DownloadMedia(reply.Media, w)
	})
	if err != nil {
		return msg.Answer(m.restoreFailure("restore_modules_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Modules restore failed:</b> <code>{}</code>", fmt.Errorf("could not download the replied file: %w", err)))
	}
	backupBytes, err = restoreModulesPayload(backupBytes)
	if err != nil {
		return msg.Answer(m.restoreFailure("restore_modules_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Modules restore failed:</b> <code>{}</code>", err))
	}

	restoreErr := m.restoreModulesFromData(backupBytes)
	if restoreErr != nil && !isForwardRestoreCommitWarning(restoreErr) {
		return msg.Answer(m.restoreFailure("restore_modules_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Modules restore failed:</b> <code>{}</code>", restoreErr))
	}

	return m.completeRestore(restoreErr, func(warning error) {
		modsRestoredTrans := m.getTrans("mods_restored", "Modules restored, restarting")
		if warning != nil {
			modsRestoredTrans += fmt.Sprintf("\n\n⚠️ Modules and database were restored, but durability could not be confirmed: %v", warning)
		}
		_ = msg.Answer(modsRestoredTrans)
	})
}

func (m *GorokuBackup) restoreAllFromZip(data []byte) error {
	if len(data) > maxRestoreCompressedBytes {
		return fmt.Errorf("backup exceeds %d compressed bytes", maxRestoreCompressedBytes)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	limits := &restoreLimits{}
	if err := limits.account(zipReader.File, false); err != nil {
		return err
	}

	entries := make(map[string]*zip.File)
	for _, file := range zipReader.File {
		if len(file.Name) > maxRestoreEntryNameBytes {
			return fmt.Errorf("backup entry name exceeds %d bytes", maxRestoreEntryNameBytes)
		}
		if file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return fmt.Errorf("unsupported backup entry %q", file.Name)
		}
		if file.Name != "db.json" && file.Name != "mods.zip" {
			return fmt.Errorf("unexpected backup entry %q", file.Name)
		}
		if _, exists := entries[file.Name]; exists {
			return fmt.Errorf("duplicate backup entry %q", file.Name)
		}
		entries[file.Name] = file
	}

	var dbBytes []byte
	var modsReader *zip.Reader
	if file := entries["db.json"]; file != nil {
		dbBytes, err = readZipFile(file)
		if err != nil {
			return err
		}
	}
	if file := entries["mods.zip"]; file != nil {
		modsZipBytes, readErr := readZipFile(file)
		if readErr != nil {
			return readErr
		}
		modsReader, err = zip.NewReader(bytes.NewReader(modsZipBytes), int64(len(modsZipBytes)))
		if err != nil {
			return err
		}
		if err := limits.account(modsReader.File, true); err != nil {
			return err
		}
	}

	if dbBytes == nil {
		return fmt.Errorf("db.json not found in archive")
	}
	backupData, err := parseRestoreDatabase(dbBytes)
	if err != nil {
		return err
	}
	m.preserveRestoreSecrets(backupData)

	var plan *moduleRestorePlan
	if modsReader != nil {
		moduleFiles, loadedMods, err := m.validateModuleRestore(modsReader.File)
		if err != nil {
			return err
		}
		loaderData := backupData["Loader"]
		if loaderData == nil {
			loaderData = make(map[string]any)
			backupData["Loader"] = loaderData
		}
		loaderData["loaded_modules"] = loadedMods
		plan, err = stageModuleRestore(runtimeModuleSourceDir(), moduleFiles)
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(plan.dir) }()
	}

	return withModuleTransaction(func() error {
		return m.applyRestore(backupData, plan)
	})
}

type moduleRestorePlan struct {
	dir   string
	names []string
}

func (m *GorokuBackup) validateModuleRestore(files []*zip.File) (map[string][]byte, map[string]string, error) {
	entries := make(map[string]*zip.File)
	for _, file := range files {
		if len(file.Name) > maxRestoreEntryNameBytes {
			return nil, nil, fmt.Errorf("module entry name exceeds %d bytes", maxRestoreEntryNameBytes)
		}
		if file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("unsupported module entry %q", file.Name)
		}
		if file.Name != "db_mods.json" && (!strings.HasSuffix(file.Name, ".go") || filepath.Base(file.Name) != file.Name) {
			return nil, nil, fmt.Errorf("invalid module path %q", file.Name)
		}
		if _, exists := entries[file.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate module entry %q", file.Name)
		}
		entries[file.Name] = file
	}

	manifestFile, ok := entries["db_mods.json"]
	if !ok {
		return nil, nil, fmt.Errorf("module manifest not found in archive")
	}
	manifestBytes, err := readZipFile(manifestFile)
	if err != nil {
		return nil, nil, err
	}
	loadedMods, err := parseModuleManifest(manifestBytes)
	if err != nil {
		return nil, nil, err
	}
	if len(loadedMods) > maxRestoreModules {
		return nil, nil, fmt.Errorf("module manifest contains more than %d modules", maxRestoreModules)
	}
	for moduleName, moduleURL := range loadedMods {
		if len(moduleName) > maxRestoreModuleNameBytes {
			return nil, nil, fmt.Errorf("module name exceeds %d bytes", maxRestoreModuleNameBytes)
		}
		if len(moduleURL) > maxRestoreModuleURLBytes {
			return nil, nil, fmt.Errorf("module URL for %q exceeds %d bytes", moduleName, maxRestoreModuleURLBytes)
		}
		if err := validateModuleProvenance(moduleName, moduleURL); err != nil {
			return nil, nil, err
		}
		if _, err := runtimeModuleSourcePath(moduleName); err != nil {
			return nil, nil, err
		}
		expectedName := moduleName + ".go"
		if entries[expectedName] == nil {
			return nil, nil, fmt.Errorf("module source %q is missing", expectedName)
		}
	}

	moduleFiles := make(map[string][]byte)
	moduleTypes := make(map[string]string)
	for name, file := range entries {
		if name == "db_mods.json" {
			continue
		}
		moduleName := strings.TrimSuffix(name, ".go")
		if _, owned := loadedMods[moduleName]; !owned {
			return nil, nil, fmt.Errorf("module source %q is not owned by the manifest", name)
		}
		if uint64(file.UncompressedSize64) > uint64(maxModuleSourceBytes) {
			return nil, nil, fmt.Errorf("module source %q exceeds %d bytes", name, maxModuleSourceBytes)
		}
		data, err := readZipFile(file)
		if err != nil {
			return nil, nil, err
		}
		structName, err := checkModuleSource(moduleName, name, data)
		if err != nil {
			return nil, nil, err
		}
		moduleFiles[name] = data
		moduleTypes[name] = structName
	}
	names := make([]string, 0, len(moduleFiles))
	for name := range moduleFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := m.validateModuleCompilation(moduleTypes[name], moduleFiles[name]); err != nil {
			return nil, nil, fmt.Errorf("invalid module source %q: %w", name, err)
		}
	}
	return moduleFiles, loadedMods, nil
}

func (m *GorokuBackup) validateModuleCompilation(structName string, source []byte) error {
	if m.compileModuleValidation != nil {
		return m.compileModuleValidation(structName, source)
	}
	return validateHotModuleCompilation(structName, source)
}

func parseModuleManifest(data []byte) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("module manifest is null")
	}
	loadedMods := make(map[string]string, len(raw))
	for moduleName, value := range raw {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("module provenance for %q is null", moduleName)
		}
		var provenance string
		if err := json.Unmarshal(value, &provenance); err != nil {
			return nil, fmt.Errorf("invalid module provenance for %q: %w", moduleName, err)
		}
		loadedMods[moduleName] = provenance
	}
	return loadedMods, nil
}

func validateModuleProvenance(moduleName, provenance string) error {
	if provenance == "local" {
		return nil
	}
	if provenance == "" || provenance != strings.TrimSpace(provenance) {
		return fmt.Errorf("module provenance for %q is empty or invalid", moduleName)
	}
	u, err := url.Parse(provenance)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("module provenance for %q is invalid", moduleName)
	}
	return nil
}

func checkModuleSource(moduleName, name string, data []byte) (string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.AllErrors)
	if err != nil {
		return "", fmt.Errorf("invalid module source %q: %w", name, err)
	}
	if parsed.Name == nil || parsed.Name.Name == "" {
		return "", fmt.Errorf("invalid module source %q: package declaration is required", name)
	}
	structName := extractStructName(data, moduleName)
	if !token.IsIdentifier(structName) {
		return "", fmt.Errorf("invalid module source %q: invalid module type name %q", name, structName)
	}
	return structName, nil
}

func stageModuleRestore(modsDir string, files map[string][]byte) (*moduleRestorePlan, error) {
	parent := filepath.Dir(modsDir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(parent, ".modules-restore-")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(stage, name), files[name], 0600); err != nil {
			_ = os.RemoveAll(stage)
			return nil, err
		}
	}
	return &moduleRestorePlan{dir: stage, names: names}, nil
}

// applyRestore mutates module sources then Database (stage then rename).
//
// Dual-commit protocol (see restore_journal.go): stage under journal/staged/,
// durable journal with restore_id + payload_hash + db_file, stage DB candidate
// (same-dir temp + journal-retained staged-db.json), apply FS (copy, keep staged),
// files_applied → db_applying → commit staged DB (rename) → primary + last-valid
// restore_id markers → db_applied → remove journal (and retained staged-db).
// Crash after DB rename but before db_applied is recovered by matching restore_id
// in live DB / primary / last-valid / markers, or by completing commit from the
// retained staged-db candidate when probes are unreadable.
func (m *GorokuBackup) applyRestore(backupData map[string]map[string]any, plan *moduleRestorePlan) error {
	// Clear any leftover incomplete restore before starting a new one.
	if err := recoverIncompleteModuleRestoreLocked(m.db); err != nil {
		return fmt.Errorf("recover incomplete module restore: %w", err)
	}

	oldDB := m.db.GetAll()
	modsDir := runtimeModuleSourceDir()
	createdModsDir := false
	var journal *restoreJournal
	var journalState *restoreJournalState
	var stagedDBPath string

	rollbackFiles := func() error {
		if journalState == nil || journal == nil {
			if stagedDBPath != "" {
				_ = m.db.AbortStagedReset(stagedDBPath)
				stagedDBPath = ""
			}
			return nil
		}
		filesErr := rollbackRestoreJournalState(journalState, journal)
		_ = journal.remove()
		journal = nil
		journalState = nil
		stagedDBPath = ""
		return filesErr
	}

	if plan != nil {
		installNames := make(map[string]struct{}, len(plan.names))
		for _, name := range plan.names {
			installNames[name] = struct{}{}
		}
		allNames := make(map[string]struct{}, len(plan.names))
		for name := range installNames {
			allNames[name] = struct{}{}
		}
		oldManifest, err := moduleManifestFromDatabase(oldDB)
		if err != nil {
			return fmt.Errorf("read current module manifest: %w", err)
		}
		desiredManifest, err := moduleManifestFromDatabase(backupData)
		if err != nil {
			return fmt.Errorf("read restored module manifest: %w", err)
		}
		for moduleName := range oldManifest {
			if _, keep := desiredManifest[moduleName]; keep {
				continue
			}
			path, err := runtimeModuleSourcePath(moduleName)
			if err != nil {
				return err
			}
			allNames[filepath.Base(path)] = struct{}{}
		}
		names := make([]string, 0, len(allNames))
		for name := range allNames {
			names = append(names, name)
		}
		sort.Strings(names)

		cleanupCreatedDir := func() {
			if createdModsDir {
				_ = os.Remove(modsDir)
			}
		}
		if info, err := os.Lstat(modsDir); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("module storage is not a directory")
			}
		} else if os.IsNotExist(err) {
			if err := os.MkdirAll(modsDir, 0700); err != nil {
				return err
			}
			createdModsDir = true
		} else {
			return err
		}

		entries := make([]restoreJournalEntry, 0, len(names))
		stagedSources := make(map[string][]byte)
		for _, name := range names {
			_, install := installNames[name]
			entry := restoreJournalEntry{Name: name, Install: install}
			destination := filepath.Join(modsDir, name)
			info, err := os.Lstat(destination)
			if err == nil {
				if !info.Mode().IsRegular() {
					cleanupCreatedDir()
					return fmt.Errorf("module destination %q is not a regular file", name)
				}
				entry.Existed = true
				entry.Mode = uint32(info.Mode().Perm())
			} else if !os.IsNotExist(err) {
				cleanupCreatedDir()
				return err
			}
			if install {
				body, err := os.ReadFile(filepath.Join(plan.dir, name)) //nolint:gosec
				if err != nil {
					cleanupCreatedDir()
					return err
				}
				stagedSources[name] = body
			}
			entries = append(entries, entry)
		}

		dbFile := ""
		if m.db != nil {
			dbFile = m.db.LocalPath()
		}
		journal = openRestoreJournal()
		if err := journal.begin(modsDir, createdModsDir, entries, stagedSources, dbFile); err != nil {
			cleanupCreatedDir()
			return fmt.Errorf("prepare restore journal: %w", err)
		}
		// Reload state so RestoreID / PayloadHash from begin are authoritative.
		journalState, err = journal.readState()
		if err != nil {
			cleanupCreatedDir()
			_ = journal.remove()
			journal = nil
			journalState = nil
			return fmt.Errorf("read restore journal after prepare: %w", err)
		}

		// Stamp dual-commit metadata before staging the DB candidate.
		backupData = stampRestoreCommitMetadata(backupData, journalState.RestoreID, journalState.PayloadHash)

		// Stage DB candidate (fsync) before any live FS mutation so rename can
		// wait until files_applied is durable. Primary remains pre-restore.
		// Keep a journal-retained copy until journal clear so recovery can
		// forward-commit if the same-dir rename candidate is consumed/lost.
		if staged, stageErr := m.db.StageReset(backupData); stageErr != nil {
			cleanupCreatedDir()
			_ = journal.remove()
			journal = nil
			journalState = nil
			return fmt.Errorf("stage database restore candidate: %w", stageErr)
		} else {
			stagedDBPath = staged
			retained, retainErr := journal.retainStagedDB(staged)
			if retainErr != nil {
				_ = m.db.AbortStagedReset(stagedDBPath)
				cleanupCreatedDir()
				_ = journal.remove()
				journal = nil
				journalState = nil
				return fmt.Errorf("retain staged database candidate: %w", retainErr)
			}
			if err := journal.setStagedDBPath(journalState, retained); err != nil {
				_ = m.db.AbortStagedReset(stagedDBPath)
				cleanupCreatedDir()
				_ = journal.remove()
				journal = nil
				journalState = nil
				return fmt.Errorf("record staged database path: %w", err)
			}
		}

		if err := journal.markApplying(journalState); err != nil {
			_ = m.db.AbortStagedReset(stagedDBPath)
			cleanupCreatedDir()
			_ = journal.remove()
			journal = nil
			journalState = nil
			return fmt.Errorf("mark restore journal applying: %w", err)
		}

		for i := range journalState.Entries {
			entry := &journalState.Entries[i]
			if !entry.Install {
				if !entry.Existed {
					continue
				}
				if err := os.Remove(filepath.Join(modsDir, entry.Name)); err != nil {
					filesErr := rollbackFiles()
					if filesErr != nil {
						return errors.Join(fmt.Errorf("module removal failed: %w", err), fmt.Errorf("file rollback failed: %w", filesErr))
					}
					return err
				}
				if err := journal.markEntryApplied(journalState, i); err != nil {
					filesErr := rollbackFiles()
					if filesErr != nil {
						return errors.Join(fmt.Errorf("journal after removal failed: %w", err), fmt.Errorf("file rollback failed: %w", filesErr))
					}
					return err
				}
				continue
			}
			source := filepath.Join(journal.stagedDir(), entry.Name)
			destination := filepath.Join(modsDir, entry.Name)
			// Copy (not consume-rename) so staged/ survives for crash recovery
			// re-apply after a DB commit that already carries restore_id.
			var applyErr error
			if m.restoreApplyFile != nil {
				applyErr = m.restoreApplyFile(source, destination)
			} else {
				body, readErr := os.ReadFile(source) //nolint:gosec
				if readErr != nil {
					applyErr = readErr
				} else {
					applyErr = writeFileDurable(destination, body, 0600)
				}
			}
			if applyErr != nil {
				filesErr := rollbackFiles()
				if filesErr != nil {
					return errors.Join(fmt.Errorf("module apply failed: %w", applyErr), fmt.Errorf("file rollback failed: %w", filesErr))
				}
				return applyErr
			}
			if err := journal.markEntryApplied(journalState, i); err != nil {
				filesErr := rollbackFiles()
				if filesErr != nil {
					return errors.Join(fmt.Errorf("journal after apply failed: %w", err), fmt.Errorf("file rollback failed: %w", filesErr))
				}
				return err
			}
		}

		// Durable files_applied before any DB rename.
		if err := journal.markFilesApplied(journalState); err != nil {
			filesErr := rollbackFiles()
			if filesErr != nil {
				return errors.Join(fmt.Errorf("journal files_applied failed: %w", err), fmt.Errorf("file rollback failed: %w", filesErr))
			}
			return err
		}
		// db_applying before DB rename: recovery uses restore_id to choose forward vs rollback.
		if err := journal.markDBApplying(journalState); err != nil {
			filesErr := rollbackFiles()
			if filesErr != nil {
				return errors.Join(fmt.Errorf("journal db_applying failed: %w", err), fmt.Errorf("file rollback failed: %w", filesErr))
			}
			return err
		}
	}

	// DB-only restore (no module plan) uses Reset; joint FS+DB uses staged commit.
	var dbErr error
	if journal != nil && journalState != nil && stagedDBPath != "" {
		if m.restoreDBReset != nil {
			// Test seam: still exercise the injected path, then drop the same-dir
			// candidate. Journal-retained staged-db stays until finish/rollback.
			dbErr = m.restoreDBReset(backupData)
			_ = m.db.AbortStagedReset(stagedDBPath)
			stagedDBPath = ""
		} else {
			dbErr = m.db.CommitStagedReset(stagedDBPath, backupData)
			if dbErr == nil {
				// Same-dir candidate consumed by rename; retained journal copy
				// stays until finishForwardCommit clears the journal.
				stagedDBPath = ""
			}
		}
	} else {
		resetDB := m.restoreDBReset
		if resetDB == nil {
			resetDB = m.db.Reset
		}
		dbErr = resetDB(backupData)
	}

	if dbErr != nil {
		var diagnostic *goroku.DatabaseError
		if errors.As(dbErr, &diagnostic) && diagnostic.Committed && errors.Is(dbErr, goroku.ErrDatabaseCommitUncertain) {
			// DB is treated as forward-committed; markers + db_applied + clear.
			if journal != nil && journalState != nil {
				_ = journal.finishForwardCommit(journalState)
			}
			return &forwardRestoreCommitWarning{err: fmt.Errorf("database restore durability warning: %w", dbErr)}
		}
		resetDB := m.restoreDBReset
		if resetDB == nil {
			resetDB = m.db.Reset
		}
		dbRollbackErr := resetDB(oldDB)
		filesErr := rollbackFiles()
		err := fmt.Errorf("database restore failed: %w", dbErr)
		if dbRollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("database rollback failed: %w", dbRollbackErr))
		}
		if filesErr != nil {
			err = errors.Join(err, fmt.Errorf("file rollback failed: %w", filesErr))
		}
		return err
	}
	if journal != nil && journalState != nil {
		// Markers next to primary + last-valid, then db_applied → remove journal
		// (retained staged-db cleared with the journal directory).
		if err := journal.finishForwardCommit(journalState); err != nil {
			// FS+DB already match backup; surface journal cleanup only if both
			// db_applied and remove failed (recovery could otherwise roll FS).
			return fmt.Errorf("finalize restore journal after db commit: %w", err)
		}
	}
	return nil
}

func moduleManifestFromDatabase(data map[string]map[string]any) (map[string]string, error) {
	loader := data["Loader"]
	if loader == nil || loader["loaded_modules"] == nil {
		return map[string]string{}, nil
	}
	manifest, err := json.Marshal(loader["loaded_modules"])
	if err != nil {
		return nil, err
	}
	return parseModuleManifest(manifest)
}

func (m *GorokuBackup) restoreModulesFromData(data []byte) error {
	if len(data) > maxRestoreCompressedBytes {
		return fmt.Errorf("backup exceeds %d compressed bytes", maxRestoreCompressedBytes)
	}

	var moduleFiles map[string][]byte
	var loadedMods map[string]string
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		limits := &restoreLimits{}
		if err := limits.account(zipReader.File, false); err != nil {
			return err
		}
		moduleFiles, loadedMods, err = m.validateModuleRestore(zipReader.File)
		if err != nil {
			return err
		}
	} else {
		loadedMods, err = parseModuleManifest(data)
		if err != nil {
			return fmt.Errorf("invalid module backup: %w", err)
		}
		if len(loadedMods) != 0 {
			return fmt.Errorf("module backup manifest has no archived sources")
		}
		moduleFiles = make(map[string][]byte)
	}

	plan, err := stageModuleRestore(runtimeModuleSourceDir(), moduleFiles)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(plan.dir) }()
	desiredDB := m.db.GetAll()
	loaderData := desiredDB["Loader"]
	if loaderData == nil {
		loaderData = make(map[string]any)
		desiredDB["Loader"] = loaderData
	}
	loaderData["loaded_modules"] = loadedMods
	return withModuleTransaction(func() error {
		return m.applyRestore(desiredDB, plan)
	})
}

// RestoreAllCmd restores both database and custom modules from a replied ZIP file.
func (m *GorokuBackup) RestoreAllCmd(msg *goroku.Message) error {
	reply, err := msg.GetReplyMessage()
	if err != nil || reply == nil || reply.Media == nil {
		replyToTrans := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		return msg.Answer(replyToTrans)
	}

	backupBytes, err := downloadRestoreMedia(func(w io.Writer) error {
		return m.client.DownloadMedia(reply.Media, w)
	})
	if err != nil {
		return msg.Answer(m.restoreFailure("restore_all_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Full backup restore failed:</b> <code>{}</code>", fmt.Errorf("could not download the replied file: %w", err)))
	}

	err = m.restoreAllFromZip(backupBytes)
	if err != nil && !isForwardRestoreCommitWarning(err) {
		return msg.Answer(m.restoreFailure("restore_all_failed", "<tg-emoji emoji-id=5210952531676504517>🚫</tg-emoji> <b>Full backup restore failed:</b> <code>{}</code>", err))
	}

	return m.completeRestore(err, func(warning error) {
		allRestoredTrans := m.getTrans("all_restored", "Your full backup has been restored, restarting...")
		if warning != nil {
			allRestoredTrans += fmt.Sprintf("\n\n⚠️ The backup was restored, but database durability could not be confirmed: %v", warning)
		}
		_ = msg.Answer(allRestoredTrans)
	})
}

// BackupAllCmd sends a zip archive containing db.json + mods/*.go files.
func (m *GorokuBackup) BackupAllCmd(msg *goroku.Message) error {
	prefix := m.commandPrefix()

	archiveBytes, err := m.buildArchive()
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to create backup archive: %v", err))
	}

	filename := fmt.Sprintf("goroku-%s.backup", time.Now().Format("02-01-2006-15-04"))

	infoTrans := m.getTrans("backupall_info", "")
	caption := strings.ReplaceAll(infoTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(archiveBytes), name: filename}

	contentChannelID, topicID := m.contentChannelOrFallback(msg.Context(), m.getBackupTopicID())

	// 1. Send file via userbot to the forum topic (or PM if topicID == 0)
	var res any
	if topicID == 0 {
		res, err = m.client.SendFile(goroku.ChatRefID(msg.ChatID), nr, caption)
	} else {
		res, err = m.client.SendFileWithOptions(goroku.ChatRefID(int64(-1000000000000-contentChannelID)),
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
	im := m.client.GorokuInline
	if im != nil && im.IsComplete() {
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

		var formTarget any = dummy
		if topicID == 0 {
			formTarget = msg
		}

		_, err = im.Form(formTextFormatted, formTarget, markup)
		if err != nil {
			L().Error("Failed to send inline restore form", zap.Error(err))
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
	im := m.client.GorokuInline
	if im != nil {
		_ = call.Edit("❓ <b>Are you sure?</b>", im.GenerateMarkup(markup))
	}
	return nil
}

func (m *GorokuBackup) handleRestoreExecuteFromMessageCallback(call inline.CallbackQuery, targetMsgID int64) error {
	msg, err := m.client.GetMessage(goroku.ChatRefID(call.ChatID), targetMsgID)
	if err != nil || msg == nil || msg.Media == nil {
		alertText := m.getTrans("reply_to_file", "Reply with .json or .zip file")
		_ = call.Answer(alertText, true)
		return nil
	}

	backupBytes, err := downloadRestoreMedia(func(w io.Writer) error {
		return m.client.DownloadMedia(msg.Media, w)
	})
	if err != nil {
		_ = call.Answer(fmt.Sprintf("Full backup download failed: %v", err), true)
		return nil
	}

	err = m.restoreAllFromZip(backupBytes)
	if err != nil && !isForwardRestoreCommitWarning(err) {
		_ = call.Answer(fmt.Sprintf("Full backup restore failed: %v", err), true)
		return nil
	}

	return m.completeRestore(err, func(warning error) {
		restoredText := m.getTrans("all_restored_bot", "Your full backup has been restored, restarting...")
		if warning != nil {
			restoredText += fmt.Sprintf("\n\n⚠️ The backup was restored, but database durability could not be confirmed: %v", warning)
		}
		_ = call.Answer(restoredText, true)
		_ = closeForm(call)
	})
}

// SetBackupPeriodCmd parses an integer number of hours and stores it.
func (m *GorokuBackup) SetBackupPeriodCmd(msg *goroku.Message) error {
	arg := strings.TrimSpace(utils.GetArgsRaw(msg.Text))

	prefix := m.commandPrefix()

	hours, err := strconv.Atoi(arg)
	if err != nil || hours < 0 || hours >= 200 {
		invalidTrans := m.getTrans("invalid_args", "🚫 <b>Please specify the correct frequency in hours, or `0` to disable</b>")
		return msg.Answer(invalidTrans)
	}

	if hours == 0 {
		if err := m.setBackupPeriod(hours, time.Now()); err != nil {
			return msg.Answer(fmt.Sprintf("❌ Failed to save backup period: %v", err))
		}

		neverTrans := m.getTrans("never", "✅ I will not make automatic backups. Can be cancelled using {prefix}set_backup_period")
		neverMsg := "<b>" + strings.ReplaceAll(neverTrans, "{prefix}", prefix) + "</b>"
		return msg.Answer(neverMsg)
	}

	if err := m.setBackupPeriod(hours, time.Now()); err != nil {
		return msg.Answer(fmt.Sprintf("❌ Failed to save backup period: %v", err))
	}

	savedTrans := m.getTrans("saved", "✅ The periodicity is saved! It can be changed with {prefix}set_backup_period")
	savedMsg := "<b>" + strings.ReplaceAll(savedTrans, "{prefix}", prefix) + "</b>"
	return msg.Answer(savedMsg)
}

func (m *GorokuBackup) setBackupPeriod(hours int, now time.Time) error {
	if hours == 0 {
		if err := m.db.Set("GorokuBackup", "period", "disabled"); err != nil {
			return err
		}
		m.backupPeriod = 0
		return nil
	}

	periodSecs := hours * 3600
	if err := m.db.Update(map[string]map[string]any{
		"GorokuBackup": {"period": float64(periodSecs), "last_backup": now.Unix()},
	}); err != nil {
		return err
	}
	m.backupPeriod = time.Duration(periodSecs) * time.Second
	m.lastBackup = now
	return nil
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
				if err := m.reloadBackupPeriod(); err != nil {
					L().Error("backup scheduler database read failed",
						zap.String("operation", "get"),
						zap.String("owner", "GorokuBackup"),
						zap.String("key", "period"),
						zap.Error(err),
					)
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
			if err := m.sendPeriodicBackupSafely(); err != nil {
				L().Error("GorokuBackup periodic backup failed", zap.Error(err))
				select {
				case <-m.stopBackup:
					return
				case <-time.After(60 * time.Second):
				}
				continue
			}
			now := time.Now()
			if err := m.db.SetInt64("GorokuBackup", "last_backup", now.Unix()); err != nil {
				L().Error("background database write failed",
					zap.String("operation", "set"),
					zap.String("owner", "GorokuBackup"),
					zap.String("key", "last_backup"),
					zap.Error(err),
				)
				continue
			}
			m.lastBackup = now
		}
	}
}

// sendPeriodicBackupSafely converts a panic in the backup path into an error so
// the scheduler retries on its normal cadence instead of taking down the
// process. The loop runs unattended for the life of the bot, so a single bad
// archive must not be fatal.
func (m *GorokuBackup) sendPeriodicBackupSafely() (err error) {
	defer func() {
		if r := recover(); r != nil {
			L().Error("panic during periodic backup", zap.Any("panic", r))
			err = fmt.Errorf("panic during periodic backup: %v", r)
		}
	}()
	return m.sendPeriodicBackup()
}

func (m *GorokuBackup) sendPeriodicBackup() error {
	archiveBytes, err := m.buildArchive()
	if err != nil {
		return fmt.Errorf("build archive: %w", err)
	}

	filename := fmt.Sprintf("backup-%s.backup", time.Now().Format("02-01-2006-15-04"))

	prefix := m.commandPrefix()
	infoTrans := m.getTrans("backupall_info", "")
	caption := strings.ReplaceAll(infoTrans, "{prefix}", utils.EscapeHTML(prefix))

	nr := &namedReader{r: bytes.NewReader(archiveBytes), name: filename}

	contentChannelID, topicID := m.contentChannelOrFallback(context.Background(), m.getBackupTopicID())

	// Send document via userbot
	var res any
	if topicID == 0 {
		res, err = m.client.SendFile(goroku.ChatRefID(m.client.TGID), nr, caption)
	} else {
		res, err = m.client.SendFileWithOptions(goroku.ChatRefID(int64(-1000000000000-contentChannelID)),
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
	im := m.client.GorokuInline
	if im != nil && im.IsComplete() {
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

const backupSecretMarkerKey = "__goroku_backup_secret__"

var backupSecretMarker = map[string]any{backupSecretMarkerKey: "omitted"}

type backupSecretPath struct {
	owner string
	key   string
}

// backupSecretPaths uses schema metadata rather than guessing from option names.
// These explicit paths are legacy secret-bearing settings without validator metadata.
func (m *GorokuBackup) backupSecretPaths() []backupSecretPath {
	paths := []backupSecretPath{
		{owner: "goroku.inline", key: "bot_token"},
		{owner: "main", key: "redis_uri"},
		{owner: "main", key: "db_uri"},
		{owner: "loader", key: "token"},
		{owner: "goroku.loader", key: "token"},
	}
	if m.client == nil || m.client.Loader == nil {
		return paths
	}
	for _, module := range m.client.Loader.GetModules() {
		withValidators, ok := module.(goroku.ModuleWithConfigValidators)
		if !ok {
			continue
		}
		for key, validator := range withValidators.ConfigValidators() {
			if goroku.IsSecretValidator(validator) {
				paths = append(paths, backupSecretPath{owner: module.Name(), key: key})
			}
		}
		if withSchema, ok := module.(goroku.ModuleWithConfigSchema); ok {
			for _, key := range goroku.SchemaSecretKeys(withSchema.ConfigSchema()) {
				paths = append(paths, backupSecretPath{owner: module.Name(), key: key})
			}
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].owner == paths[j].owner {
			return paths[i].key < paths[j].key
		}
		return paths[i].owner < paths[j].owner
	})
	return paths
}

func copyDatabaseData(data map[string]map[string]any) map[string]map[string]any {
	copyData := make(map[string]map[string]any, len(data))
	for owner, values := range data {
		copyValues := make(map[string]any, len(values))
		for key, value := range values {
			copyValues[key] = value
		}
		copyData[owner] = copyValues
	}
	return copyData
}

func findDatabasePath(data map[string]map[string]any, path backupSecretPath) (string, string, bool) {
	for owner, values := range data {
		if !strings.EqualFold(owner, path.owner) {
			continue
		}
		for key := range values {
			if strings.EqualFold(key, path.key) {
				return owner, key, true
			}
		}
		return owner, path.key, false
	}
	return path.owner, path.key, false
}

func isBackupSecretMarker(value any) bool {
	marker, ok := value.(map[string]any)
	if !ok || len(marker) != 1 {
		return false
	}
	return marker[backupSecretMarkerKey] == "omitted"
}

func (m *GorokuBackup) preserveRestoreSecrets(restored map[string]map[string]any) {
	current := m.db.GetAll()
	paths := m.backupSecretPaths()
	for owner, values := range restored {
		for key, value := range values {
			if isBackupSecretMarker(value) {
				paths = append(paths, backupSecretPath{owner: owner, key: key})
			}
		}
	}
	for _, path := range paths {
		restoredOwner, restoredKey, restoredHas := findDatabasePath(restored, path)
		currentOwner, currentKey, currentHas := findDatabasePath(current, path)
		if currentHas {
			values := restored[restoredOwner]
			if values == nil {
				values = make(map[string]any)
				restored[restoredOwner] = values
			}
			values[restoredKey] = current[currentOwner][currentKey]
		} else if restoredHas {
			delete(restored[restoredOwner], restoredKey)
		}
	}
}

// buildDBJSON serialises the database with known secrets replaced by a stable
// marker. Restore never writes that marker into the live database.
func (m *GorokuBackup) buildDBJSON() ([]byte, error) {
	data := copyDatabaseData(m.db.GetAll())
	for _, path := range m.backupSecretPaths() {
		owner, key, exists := findDatabasePath(data, path)
		if exists {
			data[owner][key] = backupSecretMarker
		}
	}
	return json.MarshalIndent(data, "", "  ")
}

func (m *GorokuBackup) buildModulesArchive() ([]byte, int, error) {
	loadedMods, err := m.loadedModulesMapChecked()
	if err != nil {
		return nil, 0, err
	}
	// localRuntimeModules keys by declared struct name, but the source path is
	// derived from the manifest key below. Keying by the file's own basename
	// keeps the two consistent; keying by struct name invented an entry whose
	// derived path did not exist whenever a file's name differed from the struct
	// it declares (owned.go declaring type Owned).
	for _, path := range localRuntimeModules() {
		name := strings.TrimSuffix(filepath.Base(path), ".go")
		if _, tracked := loadedMods[name]; !tracked {
			loadedMods[name] = "local"
		}
	}
	if len(loadedMods) > maxRestoreModules {
		return nil, 0, fmt.Errorf("module manifest contains more than %d modules", maxRestoreModules)
	}

	type moduleSource struct {
		name       string
		structName string
		body       []byte
	}
	sources := make([]moduleSource, 0, len(loadedMods))
	archivedMods := make(map[string]string, len(loadedMods))
	for modName, provenance := range loadedMods {
		if len(modName) > maxRestoreModuleNameBytes {
			return nil, 0, fmt.Errorf("module name exceeds %d bytes", maxRestoreModuleNameBytes)
		}
		if len(provenance) > maxRestoreModuleURLBytes {
			return nil, 0, fmt.Errorf("module URL for %q exceeds %d bytes", modName, maxRestoreModuleURLBytes)
		}
		if err := validateModuleProvenance(modName, provenance); err != nil {
			return nil, 0, err
		}
		path, err := runtimeModuleSourcePath(modName)
		if err != nil {
			return nil, 0, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			// Skipping here produced an archive whose manifest silently omitted the
			// module, so restoring that backup lost it without any indication. A
			// backup is complete or it fails; every other error below does the same.
			return nil, 0, fmt.Errorf("module source %q is unavailable: declared in the module manifest but missing on disk", modName+".go")
		}
		if err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("not a regular file")
			}
			return nil, 0, fmt.Errorf("module source %q is unreadable: %w", modName+".go", err)
		}
		if info.Size() > maxModuleSourceBytes {
			return nil, 0, fmt.Errorf("module source %q exceeds %d bytes", modName+".go", maxModuleSourceBytes)
		}
		body, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return nil, 0, fmt.Errorf("module source %q is unreadable: %w", modName+".go", err)
		}
		structName, err := checkModuleSource(modName, modName+".go", body)
		if err != nil {
			return nil, 0, err
		}
		sources = append(sources, moduleSource{name: modName + ".go", structName: structName, body: body})
		archivedMods[modName] = provenance
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	for _, source := range sources {
		if err := m.validateModuleCompilation(source.structName, source.body); err != nil {
			return nil, 0, fmt.Errorf("invalid module source %q: %w", source.name, err)
		}
	}

	manifest, err := json.MarshalIndent(archivedMods, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := addFileToZip(zw, "db_mods.json", manifest); err != nil {
		return nil, 0, err
	}
	for _, source := range sources {
		if err := addFileToZip(zw, source.name, source.body); err != nil {
			return nil, 0, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), len(sources), nil
}

// buildArchive creates a zip archive containing:
//   - db.json     – full database snapshot
//   - mods.zip    – zip containing loaded modules + db_mods.json
func (m *GorokuBackup) buildArchive() ([]byte, error) {
	dbJSON, err := m.buildDBJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal db: %w", err)
	}
	modsArchive, _, err := m.buildModulesArchive()
	if err != nil {
		return nil, fmt.Errorf("build modules archive: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Write db.json
	if err := addFileToZip(zw, "db.json", dbJSON); err != nil {
		return nil, err
	}

	// Write mods.zip
	if err := addFileToZip(zw, "mods.zip", modsArchive); err != nil {
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
