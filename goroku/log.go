package goroku

import (
	"context"
	"fmt"
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
	"gopkg.in/natefinch/lumberjack.v2"
)

type TelegramLogsHandler struct {
	mu        sync.Mutex
	buf       []string
	client    *CustomTelegramClient
	logChatID int64
	stopCh    chan struct{}
	active    bool
}

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
		h.stopCh = make(chan struct{})
		go h.startPolling()
	}
}

func (h *TelegramLogsHandler) startPolling() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.flush()
		case <-h.stopCh:
			h.flush()
			return
		}
	}
}

func (h *TelegramLogsHandler) flush() {
	h.mu.Lock()
	if len(h.buf) == 0 || h.client == nil || h.logChatID == 0 {
		h.mu.Unlock()
		return
	}
	records := h.buf
	h.buf = nil
	h.mu.Unlock()

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

	peer, err := h.client.ResolvePeer(h.logChatID)
	if err != nil {
		peer = &tg.InputPeerChannel{ChannelID: h.logChatID}
	}

	// Retrieve "Logs" topic ID if available from the database cache
	var topicID int64
	if h.client.GorokuDB != nil {
		if db, ok := h.client.GorokuDB.(interface {
			Get(owner, key string, defaultValue interface{}) interface{}
		}); ok {
			forumsCacheVal := db.Get("goroku.forums", "forums_cache", nil)
			if forumsCache, ok := forumsCacheVal.(map[string]interface{}); ok {
				if subCacheVal, ok := forumsCache["goroku-userbot"]; ok {
					if subCache, ok := subCacheVal.(map[string]interface{}); ok {
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
		up := uploader.NewUploader(h.client.rawAPI)
		inputFile, err := up.FromBytes(h.client.ctx, "goroku-logs.txt", []byte(allText))
		if err == nil {
			_, err = h.client.rawAPI.MessagesSendMedia(h.client.ctx, &tg.MessagesSendMediaRequest{
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
				RandomID: rand.Int63(),
			})
			if err != nil {
				log.Printf("Failed to send logs file: %v\n", err)
			}
		} else {
			log.Printf("Failed to upload logs file: %v\n", err)
		}
		return
	}

	for _, chunk := range chunks {
		msg := fmt.Sprintf("<code>%s</code>", chunk)
		_, err := h.client.rawAPI.MessagesSendMessage(h.client.ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  msg,
			ReplyTo:  replyTo,
			RandomID: rand.Int63(),
		})
		if err != nil {
			log.Printf("Failed to send logs message: %v\n", err)
		}
	}
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

var TGLogHandler *TelegramLogsHandler

func InitLogging() {
	fileWriter := &lumberjack.Logger{
		Filename:   "goroku.log",
		MaxSize:    10, // MB
		MaxBackups: 1,
		LocalTime:  true,
	}

	TGLogHandler = &TelegramLogsHandler{
		buf: make([]string, 0),
	}

	coloredStdout := &ColoredStdoutWriter{w: os.Stdout}
	log.SetOutput(io.MultiWriter(coloredStdout, fileWriter, TGLogHandler))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
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
