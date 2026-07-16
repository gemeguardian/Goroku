package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"goroku/goroku/cache"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// Extracted cache and resolve components are now in separate files cache_*.go

type forbiddenInvoker struct {
	parent tg.Invoker
	client *CustomTelegramClient
}

func (f *forbiddenInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if input != nil {
		if t, ok := input.(interface{ TypeID() uint32 }); ok {
			typeID := t.TypeID()
			for _, forbidden := range f.client.ForbiddenConstructors {
				if typeID == forbidden {
					L().Warn("Blocked forbidden constructor call", zap.Int64("type_id", int64(typeID)))
					return fmt.Errorf("constructor %d is forbidden", typeID)
				}
			}

			// Rate limiting check
			db := f.client.GorokuDB
			if db != nil {
				disableProtection := db.GetBool("APILimiter", "disable_protection", true)
				if !disableProtection {
					f.client.RatelimitMu.Lock()
					bypassed := time.Now().Before(f.client.BypassSuspendUntil)
					f.client.RatelimitMu.Unlock()

					if !bypassed {
						// If currently suspended, wait
						f.client.RatelimitMu.Lock()
						for time.Now().Before(f.client.SuspendUntil) {
							dur := time.Until(f.client.SuspendUntil)
							f.client.RatelimitMu.Unlock()
							time.Sleep(dur)
							f.client.RatelimitMu.Lock()
						}
						f.client.RatelimitMu.Unlock()

						typeName := fmt.Sprintf("%T", input)
						isTargetRequest := strings.HasPrefix(typeName, "*tg.Messages") ||
							strings.HasPrefix(typeName, "*tg.Channels") ||
							strings.HasPrefix(typeName, "*tg.Account")

						if isTargetRequest {
							f.client.RatelimitMu.Lock()
							now := time.Now()
							f.client.Ratelimiter = append(f.client.Ratelimiter, RateLimitRecord{Name: typeName, TS: now})

							// Filter records within time sample
							timeSampleSec := db.GetInt("APILimiter", "time_sample", 15)
							cutoff := now.Add(-time.Duration(timeSampleSec) * time.Second)
							var filtered []RateLimitRecord
							for _, r := range f.client.Ratelimiter {
								if r.TS.After(cutoff) {
									filtered = append(filtered, r)
								}
							}
							f.client.Ratelimiter = filtered

							threshold := db.GetInt("APILimiter", "threshold", 100)
							localFloodWait := db.GetInt("APILimiter", "local_floodwait", 30)

							if len(f.client.Ratelimiter) > threshold && !f.client.FloodWaitLock {
								f.client.FloodWaitLock = true
								f.client.SuspendUntil = now.Add(time.Duration(localFloodWait) * time.Second)

								// Copy Ratelimiter slice to prevent data race with concurrent reads/writes
								limiterCopy := make([]RateLimitRecord, len(f.client.Ratelimiter))
								copy(limiterCopy, f.client.Ratelimiter)

								f.client.RatelimitMu.Unlock()

								// Dump report and send
								reportBytes, _ := json.MarshalIndent(limiterCopy, "", "  ")
								caption := fmt.Sprintf("⚠️ <b>Goroku local floodwait triggered!</b>\n"+
									"Suspended all target calls for %d seconds to prevent API ban.", localFloodWait)

								// Send report via Bot API if available to bypass gotd suspension block, otherwise fall back to SendFile
								im := f.client.InlineManager()
								if im != nil && im.GetBotAPI() != nil {
									botClient := im.GetBotAPI()
									fb := tgbotapi.FileBytes{Name: "report.json", Bytes: reportBytes}
									go func() {
										doc := tgbotapi.NewDocument(f.client.TGID, fb)
										doc.Caption = caption
										doc.ParseMode = tgbotapi.ModeHTML
										_, _ = botClient.Send(doc)
									}()
								} else {
									go func(data []byte, capText string) {
										_, _ = f.client.SendFile(ChatRefID(f.client.TGID), data, capText)
									}(reportBytes, caption)
								}

								// Sleep
								time.Sleep(time.Duration(localFloodWait) * time.Second)

								f.client.RatelimitMu.Lock()
								f.client.FloodWaitLock = false
								f.client.Ratelimiter = nil
								f.client.RatelimitMu.Unlock()
							} else {
								f.client.RatelimitMu.Unlock()
							}
						}
					}
				}
			}
		}
	}
	err := f.parent.Invoke(ctx, input, output)
	if err != nil {
		if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
			HandleAuthKeyUnregistered(f.client.TGID, f.client.SessionPath)
		}
	}
	return err
}

func (c *CustomTelegramClient) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext connects the client and aborts authentication/startup when ctx
// is canceled. The resulting client connection also inherits ctx.
func (c *CustomTelegramClient) ConnectContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.APIID == 0 || c.APIHash == "" {
		return fmt.Errorf("telegram api_id/api_hash is not configured")
	}

	// Cancel any previous connection attempt so we don't leak goroutines or
	// race against an old client.Run.
	if err := c.Close(ctx); err != nil {
		return fmt.Errorf("close previous Telegram connection: %w", err)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	c.runMu.Lock()
	c.ctx, c.cancel = runCtx, runCancel
	c.runErr = nil
	c.runMu.Unlock()

	connectResult := make(chan error, 1)
	sessionPath := c.SessionPath
	if sessionPath == "" {
		sessionPath = filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", c.TGID))
	}
	storage := &session.FileStorage{Path: sessionPath}

	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		c.cacheEntities(e)
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(msg)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCommand(hMsg)
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		c.cacheEntities(e)
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(msg)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCommand(hMsg)
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	})

	editHandler := func(ctx context.Context, e tg.Entities, msg tg.MessageClass) error {
		c.cacheEntities(e)
		m, ok := msg.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(m)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	}

	dispatcher.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnBotInlineQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotInlineQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleInlineQuery(u)
			}
		}
		return nil
	})

	dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCallbackQuery(u)
			}
		}
		return nil
	})

	sysVer := os.Getenv("SYSTEM_VERSION")
	if sysVer == "" {
		sysVer = generateRandomSystemVersion()
	}
	client := telegram.NewClient(int(c.APIID), c.APIHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  dispatcher,
		Middlewares: []telegram.Middleware{
			telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
				return (&forbiddenInvoker{parent: next, client: c}).Invoke
			}),
		},
		Device: telegram.DeviceConfig{
			SystemVersion: sysVer,
		},
	})

	c.client = client
	c.rawAPI = client.API()
	runDone := make(chan struct{})
	c.runMu.Lock()
	c.runDone = runDone
	c.runMu.Unlock()

	go func() {
		defer close(runDone)
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				select {
				case connectResult <- err:
				default:
				}
				return err
			}

			if status.Authorized {
				me, err := client.Self(ctx)
				if err == nil {
					c.TGID = me.ID
					c.Username = me.Username
					c.GorokuMe = me
				}
				_ = c.CacheDialogs()
			}

			select {
			case connectResult <- nil:
			default:
			}
			<-ctx.Done()
			return nil
		})
		if err != nil {
			if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
				HandleAuthKeyUnregistered(c.TGID, c.SessionPath)
			}
			L().Error("gotd client run error", zap.Error(err))
			select {
			case connectResult <- err:
			default:
			}
		}
		c.runMu.Lock()
		c.runErr = err
		c.runMu.Unlock()
	}()

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	select {
	case err := <-connectResult:
		if err != nil {
			runCancel()
		}
		return err
	case <-connectCtx.Done():
		runCancel()
		return fmt.Errorf("connect Telegram client: %w", connectCtx.Err())
	}
}

func (c *CustomTelegramClient) CacheDialogs() error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
		Limit:      100,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return err
	}

	var chats []tg.ChatClass
	var users []tg.UserClass
	switch dlg := res.(type) {
	case *tg.MessagesDialogsSlice:
		chats = dlg.Chats
		users = dlg.Users
	case *tg.MessagesDialogs:
		chats = dlg.Chats
		users = dlg.Users
	}

	entities := tg.Entities{
		Users:    make(map[int64]*tg.User),
		Chats:    make(map[int64]*tg.Chat),
		Channels: make(map[int64]*tg.Channel),
	}

	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			entities.Users[user.ID] = user
		}
	}

	for _, ch := range chats {
		if chat, ok := ch.(*tg.Chat); ok {
			entities.Chats[chat.ID] = chat
		} else if channel, ok := ch.(*tg.Channel); ok {
			entities.Channels[channel.ID] = channel
		}
	}

	c.cacheEntities(entities)
	return nil
}

func (c *CustomTelegramClient) ResolveUsername(username string) (bool, error) {
	_, err := c.ResolvePeer(username)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (c *CustomTelegramClient) CheckBot(username string) (bool, error) {
	if c.InlineManager() != nil {
		return c.InlineManager().CheckBot(username)
	}
	return false, fmt.Errorf("inline manager not available or does not support CheckBot")
}

func (c *CustomTelegramClient) GetLogChatIDChecked() (int64, error) {
	if c.GorokuDB == nil {
		return 0, databaseError("get", "goroku.forums", "channel_id", "", ErrDatabaseNotInitialized, nil)
	}
	val, err := c.GorokuDB.Get("goroku.forums", "channel_id", nil)
	if err != nil {
		return 0, err
	}
	if val != nil {
		switch v := val.(type) {
		case float64:
			return int64(v), nil
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		}
	}
	return 0, nil
}

// GetLogChatID keeps the historical zero fallback. Routing code that can
// return an error should use GetLogChatIDChecked.
func (c *CustomTelegramClient) GetLogChatID() int64 {
	id, err := c.GetLogChatIDChecked()
	if err != nil {
		L().Warn("Log chat lookup failed; disabling log-chat routing", zap.Error(err))
		return 0
	}
	return id
}

func getRawChannelID(id int64) int64 {
	if id < -1000000000000 {
		return -(id + 1000000000000)
	}
	if id < 0 {
		return -id
	}
	return id
}

func (c *CustomTelegramClient) ToBotAPIChatID(id int64) int64 {
	raw := getRawChannelID(id)
	return -1000000000000 - raw
}

func isSameChat(id1, id2 int64) bool {
	return getRawChannelID(id1) == getRawChannelID(id2)
}

const telegramMessageLimit = 4096

type answerMode int

const (
	answerModeDirect answerMode = iota
	answerModeInlineList
	answerModeFile
)

type answerPlan struct {
	mode     answerMode
	pages    []string
	fileText string
}

func telegramTextLen(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func splitPlainTextForTelegram(text string, limit int) []string {
	if telegramTextLen(text) <= limit {
		return []string{text}
	}

	var chunks []string
	remaining := text
	for remaining != "" {
		if telegramTextLen(remaining) <= limit {
			chunks = append(chunks, remaining)
			break
		}

		cut := 0
		units := 0
		for idx, r := range remaining {
			rUnits := telegramTextLen(string(r))
			if units+rUnits > limit {
				break
			}
			units += rUnits
			cut = idx + len(string(r))
		}
		if cut <= 0 {
			cut = len([]rune(remaining[:1]))
		}

		splitAt := cut
		for _, sep := range []string{"\n", " "} {
			if idx := strings.LastIndex(remaining[:cut], sep); idx > 0 {
				splitAt = idx
				break
			}
		}

		chunk := strings.TrimRight(remaining[:splitAt], "\n ")
		if chunk == "" {
			chunk = remaining[:cut]
			splitAt = cut
		}
		chunks = append(chunks, chunk)
		remaining = strings.TrimLeft(remaining[splitAt:], "\n ")
	}
	return chunks
}

func planLongAnswer(rawText string, canUseInline bool) answerPlan {
	plainText, _ := parseHTML(rawText)
	if telegramTextLen(plainText) < telegramMessageLimit {
		return answerPlan{mode: answerModeDirect}
	}

	plainPages := splitPlainTextForTelegram(plainText, telegramMessageLimit)
	if canUseInline && len(plainPages) <= 10 {
		pages := make([]string, len(plainPages))
		for i, page := range plainPages {
			pages[i] = stdhtml.EscapeString(page)
		}
		return answerPlan{mode: answerModeInlineList, pages: pages}
	}

	return answerPlan{mode: answerModeFile, fileText: plainText}
}

func (m *Message) Answer(text string, opts ...MsgOption) error {
	m.Answered = true
	ctx := m.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.GrepQuery != "" {
		lines := strings.Split(text, "\n")
		var matchingLines []string
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(m.GrepQuery))

		for _, line := range lines {
			matched := false
			if err == nil {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(m.GrepQuery))
			}

			if m.GrepInvert {
				if !matched {
					matchingLines = append(matchingLines, line)
				}
			} else {
				if matched {
					if err == nil {
						line = re.ReplaceAllString(line, "<u>$0</u>")
					} else {
						line = strings.ReplaceAll(line, m.GrepQuery, "<u>"+m.GrepQuery+"</u>")
					}
					matchingLines = append(matchingLines, line)
				}
			}
		}

		if len(matchingLines) == 0 {
			text = "<i>(grep output empty)</i>"
		} else {
			text = strings.Join(matchingLines, "\n")
		}
	}

	// Apply cut (keep first N lines)
	if m.CutLines > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > m.CutLines {
			lines = lines[:m.CutLines]
		}
		text = strings.Join(lines, "\n")
	}

	plainText, _ := parseHTML(text)

	// Apply split (send as multiple messages instead of file)
	if m.SplitOutput && telegramTextLen(plainText) >= telegramMessageLimit {
		chunks := splitPlainTextForTelegram(plainText, telegramMessageLimit)
		for i, chunk := range chunks {
			chunk = stdhtml.EscapeString(chunk)
			var err error
			if i == 0 {
				if m.Out {
					_, err = m.Client.EditMessageContext(ctx, ChatRefID(m.ChatID), m.ID, chunk, opts...)
				} else {
					_, err = m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), chunk, opts...)
				}
			} else {
				_, err = m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), chunk, opts...)
			}
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return nil
	}

	plan := planLongAnswer(text, m.GrepQuery == "")
	switch plan.mode {
	case answerModeInlineList:
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.Client != nil {
			if im := m.Client.InlineManager(); im != nil && im.IsComplete() {
				if _, err := im.List(m, plan.pages); err == nil {
					return nil
				}
			}
		}
		fallthrough
	case answerModeFile:
		fileText := plan.fileText
		if fileText == "" {
			fileText = plainText
		}
		if m.Out {
			_, _ = m.Client.EditMessageContext(ctx, ChatRefID(m.ChatID), m.ID, "💾 <i>Output is too long. Sending as file...</i>")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		tmpFile, err := os.CreateTemp("", "command_result_*.txt")
		if err == nil {
			defer func() { _ = os.Remove(tmpFile.Name()) }()
			_, _ = tmpFile.WriteString(fileText)
			_ = tmpFile.Close()
			_, err = m.Client.SendFileContext(ctx, ChatRefID(m.ChatID), tmpFile.Name(), "💾 Output too long")
			return err
		}
		_, err = m.Client.SendFileContext(ctx, ChatRefID(m.ChatID), []byte(fileText), "💾 Output too long")
		return err
	}
	if m.Out {
		_, err := m.Client.EditMessageContext(ctx, ChatRefID(m.ChatID), m.ID, text, opts...)
		return err
	}
	_, err := m.Client.SendMessageWithOptionsContext(ctx, ChatRefID(m.ChatID), text, opts...)
	return err
}

func WithInvertMedia(invert bool) MsgOption {
	return func(req any) {
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			r.SetInvertMedia(invert)
		} else if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetInvertMedia(invert)
		}
	}
}

func WithNoWebpage(noWebpage bool) MsgOption {
	return func(req any) {
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			r.SetNoWebpage(noWebpage)
		} else if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetNoWebpage(noWebpage)
		}
	}
}

func WithWebPageMedia(url string, optional bool, forceLarge bool) MsgOption {
	return func(req any) {
		if url == "" {
			return
		}
		media := &tg.InputMediaWebPage{
			URL:             url,
			Optional:        optional,
			ForceLargeMedia: forceLarge,
		}
		media.SetFlags()
		if r, ok := req.(*tg.MessagesEditMessageRequest); ok {
			r.SetMedia(media)
			r.SetNoWebpage(false)
		} else if r, ok := req.(*tg.MessagesSendMediaRequest); ok {
			r.Media = media
		}
	}
}

func (c *CustomTelegramClient) ForbidConstructor(constructor uint32) {
	c.ForbiddenConstructors = append(c.ForbiddenConstructors, constructor)
}

func (c *CustomTelegramClient) ForbidConstructors(constructors []uint32) {
	c.ForbiddenConstructors = append(c.ForbiddenConstructors, constructors...)
}

// splitText splits text into chunks of at most `length` runes, preferring to
// break at newlines then spaces (mirrors utils.SmartSplit but lives in goroku pkg).
func splitText(text string, length int) []string {
	runes := []rune(text)
	if len(runes) <= length {
		return []string{text}
	}
	var res []string
	for len(runes) > 0 {
		if len(runes) <= length {
			res = append(res, string(runes))
			break
		}
		chunk := runes[:length]
		cut := -1
		for i := length - 1; i >= length/2; i-- {
			if chunk[i] == '\n' {
				cut = i + 1
				break
			}
		}
		if cut == -1 {
			for i := length - 1; i >= length/2; i-- {
				if chunk[i] == ' ' {
					cut = i + 1
					break
				}
			}
		}
		if cut == -1 {
			cut = length
		}
		res = append(res, string(runes[:cut]))
		runes = runes[cut:]
	}
	return res
}

func (c *CustomTelegramClient) GetMessage(chat ChatRef, msgID int64) (*Message, error) {
	return c.GetMessageContext(nil, chat, msgID)
}

func (c *CustomTelegramClient) GetMessageContext(ctx context.Context, chat ChatRef, msgID int64) (*Message, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peer, err := c.ResolvePeerRefContext(ctx, chat)
	if err != nil {
		return nil, err
	}

	var res tg.MessagesMessagesClass
	if peerChan, ok := peer.(*tg.InputPeerChannel); ok {
		inputChannel := &tg.InputChannel{
			ChannelID:  peerChan.ChannelID,
			AccessHash: peerChan.AccessHash,
		}
		res, err = c.rawAPI.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: inputChannel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}},
		})
	} else {
		res, err = c.rawAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}})
	}

	if err != nil {
		return nil, err
	}

	var tgMsg *tg.Message
	switch mClass := res.(type) {
	case *tg.MessagesMessagesSlice:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	case *tg.MessagesMessages:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	case *tg.MessagesChannelMessages:
		if len(mClass.Messages) > 0 {
			if m, ok := mClass.Messages[0].(*tg.Message); ok {
				tgMsg = m
			}
		}
	}

	if tgMsg == nil {
		return nil, fmt.Errorf("message not found")
	}

	hMsg := c.buildMessageFromTG(tgMsg)
	hMsg.ctx = ctx

	return hMsg, nil
}

// DownloadMedia downloads the document media of a message into a writer.
func (c *CustomTelegramClient) DownloadMedia(media tg.MessageMediaClass, writer io.Writer) error {
	return c.DownloadMediaContext(nil, media, writer)
}

func (c *CustomTelegramClient) DownloadMediaContext(ctx context.Context, media tg.MessageMediaClass, writer io.Writer) error {
	if c.rawAPI == nil {
		return ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	mediaDoc, ok := media.(*tg.MessageMediaDocument)
	if !ok {
		return fmt.Errorf("media is not a document")
	}
	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok {
		return fmt.Errorf("document media is empty or invalid")
	}

	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
	}

	_, err := downloader.NewDownloader().Download(c.rawAPI, loc).Stream(ctx, writer)
	return err
}

func (c *CustomTelegramClient) InlineQuery(botUsername string, query string, chatID int64) (*tg.MessagesBotResults, error) {
	peer, err := c.ResolvePeer(botUsername)
	if err != nil {
		return nil, err
	}

	var botUser tg.InputUserClass
	if u, ok := peer.(*tg.InputPeerUser); ok {
		botUser = &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}
	} else {
		return nil, fmt.Errorf("bot is not a user")
	}

	chatPeer, err := c.ResolvePeer(chatID)
	if err != nil {
		return nil, err
	}

	res, err := c.rawAPI.MessagesGetInlineBotResults(c.ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot:    botUser,
		Peer:   chatPeer,
		Query:  query,
		Offset: "",
	})
	return res, err
}

func (c *CustomTelegramClient) SendInlineBotResult(chatID int64, queryID int64, resultID string, replyToMsgID int64) (tg.UpdatesClass, error) {
	peer, err := c.ResolvePeer(chatID)
	if err != nil {
		return nil, err
	}

	var replyTo tg.InputReplyToClass
	if replyToMsgID != 0 {
		replyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(replyToMsgID)}
	}

	res, err := c.rawAPI.MessagesSendInlineBotResult(c.ctx, &tg.MessagesSendInlineBotResultRequest{
		Peer:     peer,
		QueryID:  queryID,
		ID:       resultID,
		RandomID: rand.Int63(), //nolint:gosec
		ReplyTo:  replyTo,
	})
	return res, err
}

func (c *CustomTelegramClient) RequestWebView(peerUsername string, platform string, url string) (string, error) {
	peer, err := c.ResolvePeer(peerUsername)
	if err != nil {
		return "", err
	}

	u, ok := peer.(*tg.InputPeerUser)
	if !ok {
		return "", fmt.Errorf("peer is not a user")
	}

	botUser := &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}

	res, err := c.rawAPI.MessagesRequestWebView(c.ctx, &tg.MessagesRequestWebViewRequest{
		Peer:        peer,
		Bot:         botUser,
		Platform:    platform,
		URL:         url,
		FromBotMenu: false,
	})
	if err != nil {
		return "", err
	}

	return res.URL, nil
}

func (c *CustomTelegramClient) FindChannelByTitle(title string) (tg.InputPeerClass, error) {
	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}

	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	var offsetDate int
	var offsetID int

	for page := 0; page < 5; page++ { // Scan up to 500 dialogs (5 pages of 100)
		res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
			Limit:      100,
			OffsetPeer: offsetPeer,
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
		})
		if err != nil {
			return nil, err
		}

		var chats []tg.ChatClass
		var messages []tg.MessageClass
		switch dlg := res.(type) {
		case *tg.MessagesDialogsSlice:
			chats = dlg.Chats
			messages = dlg.Messages
		case *tg.MessagesDialogs:
			chats = dlg.Chats
			messages = dlg.Messages
		}

		if len(chats) == 0 {
			break
		}

		c.cacheMu.Lock()
		if c.GorokuEntityCache == nil {
			c.GorokuEntityCache = make(map[cache.EntityCacheKey]cache.CacheRecordEntity)
		}
		exp := time.Now().Unix() + 86400*30
		for _, chat := range chats {
			switch ch := chat.(type) {
			case *tg.Chat:
				peer := &tg.InputPeerChat{ChatID: ch.ID}
				record := cache.CacheRecordEntity{Entity: peer, Exp: exp, TS: time.Now().Unix()}
				c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
				c.GorokuEntityCache[cache.EntityCacheKey{ID: -ch.ID}] = record
			case *tg.Channel:
				peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
				record := cache.CacheRecordEntity{Entity: peer, Exp: exp, TS: time.Now().Unix()}
				c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
				c.GorokuEntityCache[cache.EntityCacheKey{ID: cache.TelegramChannelChatID(ch.ID)}] = record
				if ch.Username != "" {
					c.GorokuEntityCache[cache.EntityCacheKey{Username: strings.ToLower(ch.Username)}] = record
				}
			}
		}
		c.cacheMu.Unlock()

		for _, chat := range chats {
			var chatTitle string
			switch ch := chat.(type) {
			case *tg.Chat:
				chatTitle = ch.Title
			case *tg.Channel:
				chatTitle = ch.Title
			case *tg.ChatForbidden:
				chatTitle = ch.Title
			case *tg.ChannelForbidden:
				chatTitle = ch.Title
			}
			if chatTitle == title {
				switch ch := chat.(type) {
				case *tg.Chat:
					return &tg.InputPeerChat{ChatID: ch.ID}, nil
				case *tg.Channel:
					return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
				}
			}
		}

		// Paginate to next page
		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			if msg, ok := lastMsg.(*tg.Message); ok {
				offsetDate = msg.Date
				offsetID = msg.ID
				offsetPeer, _ = c.ResolvePeer(msg.PeerID)
			} else if msg, ok := lastMsg.(*tg.MessageService); ok {
				offsetDate = msg.Date
				offsetID = msg.ID
				offsetPeer, _ = c.ResolvePeer(msg.PeerID)
			} else {
				break
			}
		} else {
			break
		}
	}

	return nil, fmt.Errorf("channel not found")
}

func (c *CustomTelegramClient) CreateChannel(title, description string, megagroup, forum bool) (tg.InputPeerClass, error) {
	if c.rawAPI == nil {
		return nil, fmt.Errorf("client not connected")
	}
	res, err := c.rawAPI.ChannelsCreateChannel(c.ctx, &tg.ChannelsCreateChannelRequest{
		Title:     title,
		About:     description,
		Megagroup: megagroup,
		Forum:     forum,
	})
	if err != nil {
		return nil, err
	}

	var createdChat tg.ChatClass
	switch upd := res.(type) {
	case *tg.Updates:
		if len(upd.Chats) > 0 {
			createdChat = upd.Chats[0]
		}
	case *tg.UpdatesCombined:
		if len(upd.Chats) > 0 {
			createdChat = upd.Chats[0]
		}
	}

	if createdChat == nil {
		return nil, fmt.Errorf("no chat created in updates")
	}

	c.cacheMu.Lock()
	if c.GorokuEntityCache == nil {
		c.GorokuEntityCache = make(map[cache.EntityCacheKey]cache.CacheRecordEntity)
	}
	exp := time.Now().Unix() + 86400*30
	switch ch := createdChat.(type) {
	case *tg.Chat:
		peer := &tg.InputPeerChat{ChatID: ch.ID}
		record := cache.CacheRecordEntity{Entity: peer, Exp: exp, TS: time.Now().Unix()}
		c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
		c.GorokuEntityCache[cache.EntityCacheKey{ID: -ch.ID}] = record
	case *tg.Channel:
		peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
		record := cache.CacheRecordEntity{Entity: peer, Exp: exp, TS: time.Now().Unix()}
		c.GorokuEntityCache[cache.EntityCacheKey{ID: ch.ID}] = record
		c.GorokuEntityCache[cache.EntityCacheKey{ID: cache.TelegramChannelChatID(ch.ID)}] = record
		if ch.Username != "" {
			c.GorokuEntityCache[cache.EntityCacheKey{Username: strings.ToLower(ch.Username)}] = record
		}
	}
	c.cacheMu.Unlock()

	switch ch := createdChat.(type) {
	case *tg.Chat:
		return &tg.InputPeerChat{ChatID: ch.ID}, nil
	case *tg.Channel:
		return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
	}

	return nil, fmt.Errorf("unknown chat type created")
}

func (c *CustomTelegramClient) InviteBotToChannel(channelPeer tg.InputPeerClass) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	var botUser tg.InputUserClass
	if c.InlineManager() != nil {
		botUsername := c.InlineManager().BotUsernameStr()
		peer, err := c.ResolvePeer(botUsername)
		if err == nil {
			if u, ok := peer.(*tg.InputPeerUser); ok {
				botUser = &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}
			}
		}
	}
	if botUser == nil {
		return fmt.Errorf("bot user not found or unresolved")
	}

	resolvedPeer, err := c.ResolvePeer(channelPeer)
	if err != nil {
		return fmt.Errorf("failed to resolve channel peer: %w", err)
	}

	var inputChannel tg.InputChannelClass
	if ch, ok := resolvedPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return fmt.Errorf("peer is not a channel")
	}

	_, err = c.rawAPI.ChannelsInviteToChannel(c.ctx, &tg.ChannelsInviteToChannelRequest{
		Channel: inputChannel,
		Users:   []tg.InputUserClass{botUser},
	})
	return err
}

func (c *CustomTelegramClient) PromoteBotToAdmin(channelPeer tg.InputPeerClass) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	var botUser tg.InputUserClass
	if c.InlineManager() != nil {
		botUsername := c.InlineManager().BotUsernameStr()
		peer, err := c.ResolvePeer(botUsername)
		if err == nil {
			if u, ok := peer.(*tg.InputPeerUser); ok {
				botUser = &tg.InputUser{UserID: u.UserID, AccessHash: u.AccessHash}
			}
		}
	}
	if botUser == nil {
		return fmt.Errorf("bot user not found or unresolved")
	}

	resolvedPeer, err := c.ResolvePeer(channelPeer)
	if err != nil {
		return fmt.Errorf("failed to resolve channel peer: %w", err)
	}

	var inputChannel tg.InputChannelClass
	if ch, ok := resolvedPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return fmt.Errorf("peer is not a channel")
	}

	_, err = c.rawAPI.ChannelsEditAdmin(c.ctx, &tg.ChannelsEditAdminRequest{
		Channel: inputChannel,
		UserID:  botUser,
		AdminRights: tg.ChatAdminRights{
			ChangeInfo:     true,
			PostMessages:   true,
			EditMessages:   true,
			DeleteMessages: true,
			BanUsers:       true,
			InviteUsers:    true,
			PinMessages:    true,
			AddAdmins:      false,
			Anonymous:      false,
			ManageCall:     true,
			Other:          true,
			ManageTopics:   true,
		},
		Rank: "Goroku Bot",
	})
	return err
}

func (c *CustomTelegramClient) ToggleForum(channelPeer tg.InputPeerClass, enabled bool) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return fmt.Errorf("peer is not a channel")
	}

	_, err := c.rawAPI.ChannelsToggleForum(c.ctx, &tg.ChannelsToggleForumRequest{
		Channel: inputChannel,
		Enabled: enabled,
	})
	return err
}

func (c *CustomTelegramClient) CreateForumTopic(channelPeer tg.InputPeerClass, title, description string, iconEmojiID int64) (int64, error) {
	if c.rawAPI == nil {
		return 0, fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	var peer tg.InputPeerClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
		peer = ch
	} else {
		return 0, fmt.Errorf("peer is not a channel")
	}

	req := &tg.ChannelsCreateForumTopicRequest{
		Channel:  inputChannel,
		Title:    title,
		RandomID: rand.Int63(), //nolint:gosec
	}

	var premium bool
	if c.GorokuMe != nil {
		premium = c.GorokuMe.Premium
	}

	if premium && iconEmojiID != 0 {
		req.SetIconEmojiID(iconEmojiID)
	}

	res, err := c.rawAPI.ChannelsCreateForumTopic(c.ctx, req)
	if err != nil {
		return 0, err
	}

	var msgID int
	if upd, ok := res.(*tg.Updates); ok {
		for _, u := range upd.Updates {
			switch ut := u.(type) {
			case *tg.UpdateMessageID:
				msgID = ut.ID
			case *tg.UpdateNewChannelMessage:
				if msg, ok := ut.Message.(*tg.Message); ok {
					msgID = msg.ID
				}
			}
		}
	}

	if msgID == 0 {
		return 0, fmt.Errorf("failed to retrieve topic ID from updates")
	}

	if description != "" {
		replyTo := &tg.InputReplyToMessage{
			ReplyToMsgID: msgID,
		}
		replyTo.SetTopMsgID(msgID)
		_, _ = c.rawAPI.MessagesSendMessage(c.ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  description,
			ReplyTo:  replyTo,
			RandomID: rand.Int63(), //nolint:gosec
		})
	}

	return int64(msgID), nil
}

func (c *CustomTelegramClient) SearchForumTopic(channelPeer tg.InputPeerClass, title string) (int64, error) {
	if c.rawAPI == nil {
		return 0, fmt.Errorf("client not connected")
	}
	var inputChannel tg.InputChannelClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		inputChannel = &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}
	} else {
		return 0, fmt.Errorf("peer is not a channel")
	}

	res, err := c.rawAPI.ChannelsGetForumTopics(c.ctx, &tg.ChannelsGetForumTopicsRequest{
		Channel: inputChannel,
		Limit:   100,
	})
	if err != nil {
		return 0, err
	}

	for _, topicClass := range res.Topics {
		if topic, ok := topicClass.(*tg.ForumTopic); ok {
			if topic.Title == title {
				return int64(topic.ID), nil
			}
		}
	}
	return 0, fmt.Errorf("topic not found")
}

func (c *CustomTelegramClient) CreateGorokuFolder(botID int64) error {
	if c.rawAPI == nil {
		return fmt.Errorf("client not connected")
	}

	filters, err := c.rawAPI.MessagesGetDialogFilters(c.ctx)
	if err != nil {
		return err
	}

	folderID := 2
	for _, fClass := range filters.Filters {
		if df, ok := fClass.(*tg.DialogFilter); ok {
			if strings.TrimSpace(df.Title.Text) == "Goroku" {
				return nil // Goroku folder already exists
			}
			if df.ID >= folderID {
				folderID = df.ID + 1
			}
		}
	}

	res, err := c.rawAPI.MessagesGetDialogs(c.ctx, &tg.MessagesGetDialogsRequest{
		Limit:      100,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return err
	}

	var chats []tg.ChatClass
	var users []tg.UserClass
	switch dlg := res.(type) {
	case *tg.MessagesDialogsSlice:
		chats = dlg.Chats
		users = dlg.Users
	case *tg.MessagesDialogs:
		chats = dlg.Chats
		users = dlg.Users
	}

	var includePeers []tg.InputPeerClass
	var pinnedPeers []tg.InputPeerClass

	if botID != 0 {
		for _, u := range users {
			if user, ok := u.(*tg.User); ok && user.ID == botID {
				inlineBotPeer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
				pinnedPeers = append(pinnedPeers, inlineBotPeer)
				includePeers = append(includePeers, inlineBotPeer)
				break
			}
		}
	}

	officialIDs := map[int64]bool{
		2445389036: true,
		2341345589: true,
		2410964167: true,
	}

	for _, chat := range chats {
		var title string
		var isChannel bool
		var chatID int64
		var accessHash int64

		switch ch := chat.(type) {
		case *tg.Chat:
			title = ch.Title
			chatID = ch.ID
		case *tg.Channel:
			title = ch.Title
			chatID = ch.ID
			accessHash = ch.AccessHash
			isChannel = true
		}

		titleLower := strings.ToLower(title)
		match := strings.Contains(titleLower, "goroku") || officialIDs[chatID]
		if match {
			if isChannel {
				includePeers = append(includePeers, &tg.InputPeerChannel{ChannelID: chatID, AccessHash: accessHash})
			} else {
				includePeers = append(includePeers, &tg.InputPeerChat{ChatID: chatID})
			}
		}
	}

	_, err = c.rawAPI.MessagesUpdateDialogFilter(c.ctx, &tg.MessagesUpdateDialogFilterRequest{
		ID: folderID,
		Filter: &tg.DialogFilter{
			ID:              folderID,
			Title:           tg.TextWithEntities{Text: "Goroku"},
			Emoticon:        "🐱",
			PinnedPeers:     pinnedPeers,
			IncludePeers:    includePeers,
			ExcludePeers:    []tg.InputPeerClass{},
			ExcludeMuted:    false,
			ExcludeRead:     false,
			ExcludeArchived: false,
		},
	})
	return err
}

func parseHTML(htmlText string) (string, []tg.MessageEntityClass) {
	reEmoji := regexp.MustCompile(`(?i)<emoji\s+document_id=["']?([0-9]+)["']?>(.*?)</emoji>`)
	htmlText = reEmoji.ReplaceAllString(htmlText, `<tg-emoji emoji-id="$1">$2</tg-emoji>`)

	// Workaround gotd HTML parser bug: move trailing whitespaces out of closing tags to prevent entity length corruption
	reSpaceTag := regexp.MustCompile(`(?i)(\s+)(</(?:b|i|u|s|code|pre|tg-emoji|emoji|blockquote|tg-spoiler|spoiler)>)`)
	htmlText = reSpaceTag.ReplaceAllString(htmlText, `$2$1`)

	resolver := func(id int64) (tg.InputUserClass, error) {
		return &tg.InputUser{UserID: id}, nil
	}
	var b entity.Builder
	opt := html.String(resolver, htmlText)
	err := styling.Perform(&b, opt)
	if err != nil {
		return htmlText, nil
	}
	text, entities := b.Complete()
	return text, entities
}

func (c *CustomTelegramClient) Translate(chat ChatRef, msgID int, toLang string) (string, error) {
	if c.rawAPI == nil {
		return "", fmt.Errorf("client not connected")
	}
	peer, err := c.ResolvePeerRef(chat)
	if err != nil {
		return "", err
	}
	res, err := c.rawAPI.MessagesTranslateText(c.ctx, &tg.MessagesTranslateTextRequest{
		Peer:   peer,
		ID:     []int{msgID},
		ToLang: toLang,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, tw := range res.Result {
		sb.WriteString(entitiesToHTML(tw.Text, tw.Entities))
	}
	return sb.String(), nil
}

func (c *CustomTelegramClient) TranslateText(chat ChatRef, text string, entities []tg.MessageEntityClass, toLang string) (string, error) {
	if c.rawAPI == nil {
		return "", fmt.Errorf("client not connected")
	}
	req := &tg.MessagesTranslateTextRequest{ToLang: toLang}
	req.SetText([]tg.TextWithEntities{{Text: text, Entities: entities}})
	L().Debug("TranslateText request", zap.String("text", text), zap.Int("entities", len(entities)), zap.String("lang", toLang))
	res, err := c.rawAPI.MessagesTranslateText(c.ctx, req)
	if err != nil {
		L().Error("TranslateText failed", zap.Error(err))
		return "", err
	}
	var sb strings.Builder
	for _, tw := range res.Result {
		L().Debug("TranslateText response", zap.String("text", tw.Text), zap.Int("entities", len(tw.Entities)))
		htmlText := tw.Text
		if len(tw.Entities) > 0 {
			htmlText = entitiesToHTML(tw.Text, tw.Entities)
		}
		L().Debug("TranslateText HTML", zap.String("html", htmlText))
		sb.WriteString(htmlText)
	}
	return sb.String(), nil
}

func describeTGEntities(entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(entities))
	for _, entity := range entities {
		offset, length, _ := messageEntitySpan(entity)
		parts = append(parts, fmt.Sprintf("%T(offset=%d,length=%d)", entity, offset, length))
	}
	return strings.Join(parts, ", ")
}

func messageEntitySpan(entity tg.MessageEntityClass) (int, int, bool) {
	switch e := entity.(type) {
	case *tg.MessageEntityBold:
		return e.Offset, e.Length, true
	case *tg.MessageEntityItalic:
		return e.Offset, e.Length, true
	case *tg.MessageEntityUnderline:
		return e.Offset, e.Length, true
	case *tg.MessageEntityStrike:
		return e.Offset, e.Length, true
	case *tg.MessageEntityCode:
		return e.Offset, e.Length, true
	case *tg.MessageEntityPre:
		return e.Offset, e.Length, true
	case *tg.MessageEntitySpoiler:
		return e.Offset, e.Length, true
	case *tg.MessageEntityBlockquote:
		return e.Offset, e.Length, true
	case *tg.MessageEntityTextURL:
		return e.Offset, e.Length, true
	case *tg.MessageEntityMentionName:
		return e.Offset, e.Length, true
	case *tg.MessageEntityCustomEmoji:
		return e.Offset, e.Length, true
	}
	return 0, 0, false
}
