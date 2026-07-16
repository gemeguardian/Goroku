package goroku

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"gopkg.in/natefinch/lumberjack.v2"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

type TelegramLogsHandler struct {
	mu              sync.Mutex
	flushMu         sync.Mutex
	buf             []string
	client          *CustomTelegramClient
	logChatID       int64
	stopCh          chan struct{}
	done            chan struct{}
	active          bool
	closing         bool
	flushErr        error
	closeErr        error
	deliver         func([]string) error
	previousOutput  io.Writer
	previousFlags   int
	previousOwned   bool
	installedOutput *ownedLogWriter
}

type ownedLogWriter struct{ io.Writer }

func (h *TelegramLogsHandler) Write(p []byte) (n int, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	msg := string(p)
	if FilterLogMessage(msg) {
		return len(p), nil
	}

	h.buf = append(h.buf, msg)
	if len(h.buf) > 7000 {
		h.buf = h.buf[1:]
	}
	return len(p), nil
}

func (h *TelegramLogsHandler) InstallTGLog(client *CustomTelegramClient, logChatID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = client
	h.logChatID = logChatID
	if !h.active {
		h.active = true
		h.closing = false
		h.flushErr = nil
		h.closeErr = nil
		h.stopCh = make(chan struct{})
		h.done = make(chan struct{})
		go h.startPolling(h.stopCh, h.done)
	}
}

func (h *TelegramLogsHandler) startPolling(stop <-chan struct{}, done chan<- struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer func() {
		ticker.Stop()
		h.mu.Lock()
		h.active = false
		h.closing = false
		h.mu.Unlock()
		close(done)
	}()
	for {
		select {
		case <-ticker.C:
			_ = h.flush()
		case <-stop:
			err := h.flush()
			h.mu.Lock()
			h.closeErr = err
			h.mu.Unlock()
			return
		}
	}
}

// Close stops polling, performs its final flush, and waits for completion.
func (h *TelegramLogsHandler) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	if !h.active {
		err := h.closeErr
		h.mu.Unlock()
		return err
	}
	if !h.closing {
		h.closing = true
		close(h.stopCh)
	}
	done := h.done
	h.mu.Unlock()
	select {
	case <-done:
		h.mu.Lock()
		err := h.closeErr
		h.mu.Unlock()
		return err
	default:
	}
	select {
	case <-done:
		h.mu.Lock()
		err := h.closeErr
		h.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *TelegramLogsHandler) flush() error {
	h.flushMu.Lock()
	defer h.flushMu.Unlock()
	h.mu.Lock()
	if h.flushErr != nil {
		err := h.flushErr
		h.mu.Unlock()
		return err
	}
	if len(h.buf) == 0 {
		h.mu.Unlock()
		return nil
	}
	records := append([]string(nil), h.buf...)
	deliver := h.deliver
	h.mu.Unlock()
	if deliver == nil {
		deliver = h.deliverRecords
	}
	if err := deliver(records); err != nil {
		h.mu.Lock()
		h.flushErr = err
		h.mu.Unlock()
		return err
	}
	h.mu.Lock()
	if len(h.buf) >= len(records) {
		h.buf = h.buf[len(records):]
	}
	h.mu.Unlock()
	return nil
}

func (h *TelegramLogsHandler) deliverRecords(records []string) error {
	h.mu.Lock()
	client := h.client
	logChatID := h.logChatID
	h.mu.Unlock()
	if client == nil || logChatID == 0 {
		return fmt.Errorf("Telegram log destination is not configured")
	}

	var chunks []string
	var current strings.Builder
	for _, r := range records {
		if current.Len()+len(r) > 4000 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		current.WriteString(r)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	peer, err := client.ResolvePeer(logChatID)
	if err != nil {
		cid := logChatID
		if cid < -1000000000000 {
			cid = -cid - 1000000000000
		} else if cid < 0 {
			cid = -cid
		}
		peer = &tg.InputPeerChannel{ChannelID: cid}
	}

	// Retrieve "Logs" topic ID if available from the database cache
	var topicID int64
	if client.GorokuDB != nil {
		forumsCacheVal, readErr := client.GorokuDB.Get("goroku.forums", "forums_cache", nil)
		if readErr != nil {
			// Telegram logging cannot return this error and logging through the
			// standard logger here would feed this handler recursively.
			L().Warn("Log topic lookup failed; sending without a topic", zap.Error(readErr))
		}
		if forumsCache, ok := forumsCacheVal.(map[string]any); ok {
			if subCacheVal, ok := forumsCache["goroku-userbot"]; ok {
				if subCache, ok := subCacheVal.(map[string]any); ok {
					if idVal, ok := subCache["Logs"]; ok {
						switch idt := idVal.(type) {
						case float64:
							topicID = int64(idt)
						case int64:
							topicID = idt
						case int:
							topicID = int64(idt)
						}
					}
				}
			}
		}
	}

	// Route logs through the helper inline bot if it is complete and ready
	var botClient *tgbotapi.BotAPI
	if client.GorokuInline != nil {
		if client.GorokuInline.IsComplete() {
			botClient = client.GorokuInline.GetBotAPI()
		}
	}

	if botClient != nil {
		targetBotChatID := client.ToBotAPIChatID(logChatID)
		if len(chunks) > 5 {
			allText := strings.Join(records, "")
			fileBytes := tgbotapi.FileBytes{Name: "goroku-logs.txt", Bytes: []byte(allText)}
			_, err = SendDocumentWithTopic(botClient, targetBotChatID, fileBytes, "📋 Goroku Logs (too large to send as text)", int(topicID))
			if err != nil {
				L().Info("Failed to send logs file via bot: {0}", zap.Any("arg0", err))
			}
			return err
		}

		var sendErrs []error
		for _, chunk := range chunks {
			msgText := fmt.Sprintf("<code>%s</code>", html.EscapeString(chunk))
			_, err = SendMessageWithTopic(botClient, targetBotChatID, msgText, int(topicID))
			if err != nil {
				L().Info("Failed to send logs message via bot: {0}", zap.Any("arg0", err))
				sendErrs = append(sendErrs, err)
			}
		}
		return errors.Join(sendErrs...)
	}

	var replyTo tg.InputReplyToClass
	if topicID != 0 {
		msg := &tg.InputReplyToMessage{
			ReplyToMsgID: int(topicID),
		}
		msg.SetTopMsgID(int(topicID))
		replyTo = msg
	}

	if len(chunks) > 5 {
		allText := strings.Join(records, "")
		if client.rawAPI == nil {
			return ErrClientNotInitialized
		}
		up := uploader.NewUploader(client.rawAPI)
		inputFile, err := up.FromBytes(client.ctx, "goroku-logs.txt", []byte(allText))
		if err == nil {
			_, err = client.rawAPI.MessagesSendMedia(client.ctx, &tg.MessagesSendMediaRequest{
				Peer: peer,
				Media: &tg.InputMediaUploadedDocument{
					File:     inputFile,
					MimeType: "text/plain",
					Attributes: []tg.DocumentAttributeClass{
						&tg.DocumentAttributeFilename{FileName: "goroku-logs.txt"},
					},
				},
				Message:  "📋 Goroku Logs (too large to send as text)",
				ReplyTo:  replyTo,
				RandomID: rand.Int63(), //nolint:gosec
			})
			if err != nil {
				L().Info("Failed to send logs file: {0}", zap.Any("arg0", err))
			}
		} else {
			L().Info("Failed to upload logs file: {0}", zap.Any("arg0", err))
		}
		return err
	}

	if client.rawAPI == nil {
		return ErrClientNotInitialized
	}
	var sendErrs []error
	for _, chunk := range chunks {
		msg := fmt.Sprintf("<code>%s</code>", chunk)
		plainText, entities := parseHTML(msg)
		_, err := client.rawAPI.MessagesSendMessage(client.ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  plainText,
			Entities: entities,
			ReplyTo:  replyTo,
			RandomID: rand.Int63(), //nolint:gosec
		})
		if err != nil {
			L().Info("Failed to send logs message: {0}", zap.Any("arg0", err))
			sendErrs = append(sendErrs, err)
		}
	}
	return errors.Join(sendErrs...)
}

func (h *TelegramLogsHandler) Dump() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	res := make([]string, len(h.buf))
	copy(res, h.buf)
	return res
}

func (h *TelegramLogsHandler) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf = nil
}

func OverrideText(err error) string {
	if err == nil {
		return ""
	}
	errStr := err.Error()
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "network") {
		return "✈️ <b>You have problems with internet connection on your server.</b>"
	}
	if strings.Contains(errStr, "datacenter") {
		return "✈️ <b>Telegram has problems with their datacenters.</b>"
	}
	if strings.Contains(errStr, "overwrite") {
		return fmt.Sprintf("⚠️ %s", errStr)
	}
	return ""
}

func CheckBranchLog(meID int64, allowedIDs []int64) {
	if os.Getenv("GOROKU_NO_GIT") == "1" {
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	repoPath := filepath.Dir(filepath.Dir(execPath))
	CheckBranch(meID, allowedIDs)
	_ = repoPath
}

var logRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) ([^:]+:\d+): ([\s\S]*)$`)

type ColoredStdoutWriter struct {
	w io.Writer
}

func (cw *ColoredStdoutWriter) Write(p []byte) (n int, err error) {
	str := string(p)
	matches := logRegex.FindStringSubmatch(str)
	if len(matches) == 4 {
		timestamp := matches[1]
		fileLoc := matches[2]
		message := matches[3]

		hasNewline := strings.HasSuffix(message, "\n")
		if hasNewline {
			message = strings.TrimSuffix(message, "\n")
		}

		// Determine message color
		msgLower := strings.ToLower(message)
		colorCode := "\033[37m" // Default white
		if strings.Contains(msgLower, "fail") || strings.Contains(msgLower, "error") || strings.Contains(msgLower, "corrupt") {
			colorCode = "\033[91m" // Red
		} else if strings.Contains(msgLower, "success") || strings.Contains(msgLower, "ready") || strings.Contains(msgLower, "started") {
			colorCode = "\033[92m" // Green
		} else if strings.Contains(msgLower, "warn") {
			colorCode = "\033[93m" // Yellow
		} else if strings.Contains(msgLower, "booting") || strings.Contains(msgLower, "starting") || strings.Contains(msgLower, "creating") {
			colorCode = "\033[96m" // Cyan
		}

		// Format: timestamp [file] message
		formatted := fmt.Sprintf("\033[90m%s\033[0m \033[34m[%s]\033[0m %s%s\033[0m", timestamp, fileLoc, colorCode, message)
		if hasNewline {
			formatted += "\n"
		}
		_, err = cw.w.Write([]byte(formatted))
		return len(p), err
	}

	return cw.w.Write(p)
}

var (
	TGLogHandler *TelegramLogsHandler
	loggingMu    sync.Mutex
)

func InitLogging() *TelegramLogsHandler {
	InitZapLogging()

	fileWriter := &lumberjack.Logger{
		Filename:   "goroku.log",
		MaxSize:    10, // MB
		MaxBackups: 1,
		LocalTime:  true,
	}

	handler := &TelegramLogsHandler{buf: make([]string, 0)}

	coloredStdout := &ColoredStdoutWriter{w: os.Stdout}
	loggingMu.Lock()
	handler.previousOutput = log.Writer()
	handler.previousFlags = log.Flags()
	_, handler.previousOwned = handler.previousOutput.(*ownedLogWriter)
	handler.installedOutput = &ownedLogWriter{Writer: io.MultiWriter(coloredStdout, fileWriter, handler)}
	TGLogHandler = handler
	log.SetOutput(handler.installedOutput)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	loggingMu.Unlock()
	return handler
}

func releaseLogging(handler *TelegramLogsHandler) {
	if handler == nil {
		return
	}
	loggingMu.Lock()
	if TGLogHandler == handler {
		TGLogHandler = nil
		if log.Writer() == handler.installedOutput {
			if handler.previousOwned {
				log.SetOutput(os.Stderr)
				log.SetFlags(log.LstdFlags)
			} else {
				log.SetOutput(handler.previousOutput)
				log.SetFlags(handler.previousFlags)
			}
		}
	}
	loggingMu.Unlock()
}

var cleanLogRegex = regexp.MustCompile(`(?i)(Failed to fetch updates|Sleep)`)

func FilterLogMessage(msg string) bool {
	return cleanLogRegex.MatchString(msg)
}

type CoreOverwriteError struct {
	Message string
}

func (e *CoreOverwriteError) Error() string {
	return e.Message
}

func RunContext(ctx context.Context, fn func()) {
	fn()
}
