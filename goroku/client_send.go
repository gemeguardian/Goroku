package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goroku/goroku/utils"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

func GetSentMessageID(resp any) int64 {
	switch v := resp.(type) {
	case *tg.Updates:
		for _, update := range v.Updates {
			if u, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateEditMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateEditChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range v.Updates {
			if u, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			} else if u, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := u.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return int64(v.ID)
	case *tg.UpdateShortMessage:
		return int64(v.ID)
	case *tg.UpdateShortChatMessage:
		return int64(v.ID)
	case *tg.UpdateShort:
		if u, ok := v.Update.(*tg.UpdateNewMessage); ok {
			if msg, ok := u.Message.(*tg.Message); ok {
				return int64(msg.ID)
			}
		} else if u, ok := v.Update.(*tg.UpdateNewChannelMessage); ok {
			if msg, ok := u.Message.(*tg.Message); ok {
				return int64(msg.ID)
			}
		}
	case tgbotapi.Message:
		return int64(v.MessageID)
	case *tgbotapi.Message:
		return int64(v.MessageID)
	}
	return 0
}

func WithReplyTo(msgID int64) MsgOption {
	return func(req any) {
		if msgID == 0 {
			return
		}
		if r, ok := req.(*tg.MessagesSendMessageRequest); ok {
			r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(msgID)}
		} else if r, ok := req.(*tg.MessagesSendMediaRequest); ok {
			r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(msgID)}
		}
	}
}

func (c *CustomTelegramClient) SendFile(chat ChatRef, file any, caption string) (any, error) {
	return c.SendFileWithOptions(chat, file, caption)
}

func (c *CustomTelegramClient) SendFileWithOptions(chat ChatRef, file any, caption string, opts ...MsgOption) (any, error) {
	return c.SendFileWithOptionsContext(nil, chat, file, caption, opts...)
}

func (c *CustomTelegramClient) SendFileContext(ctx context.Context, chat ChatRef, file any, caption string) (any, error) {
	return c.SendFileWithOptionsContext(ctx, chat, file, caption)
}

func (c *CustomTelegramClient) SendFileWithOptionsContext(ctx context.Context, chat ChatRef, file any, caption string, opts ...MsgOption) (any, error) {
	return c.sendFileInternal(c.rpcContext(ctx), chat, file, caption, opts...)
}

func (c *CustomTelegramClient) resolveRequestPeer(chat ChatRef) (tg.InputPeerClass, error) {
	return c.resolveRequestPeerContext(nil, chat)
}

func (c *CustomTelegramClient) resolveRequestPeerContext(ctx context.Context, chat ChatRef) (tg.InputPeerClass, error) {
	peer, err := c.ResolvePeerRefContext(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("resolve peer: %w", err)
	}
	return peer, nil
}

func (c *CustomTelegramClient) sendFileInternal(ctx context.Context, chat ChatRef, file any, caption string, opts ...MsgOption) (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var targetChatID int64
	if !chat.IsZero() {
		switch {
		case chat.Peer() != nil:
			if u, ok := chat.Peer().(*tg.InputPeerUser); ok {
				targetChatID = u.UserID
			}
		case chat.Username() != "":
			if id, err := strconv.ParseInt(chat.Username(), 10, 64); err == nil {
				targetChatID = id
			}
		default:
			targetChatID = chat.ID()
		}
	}

	logChatID, err := c.GetLogChatIDChecked()
	if err != nil {
		return nil, fmt.Errorf("get log chat ID: %w", err)
	}
	if logChatID != 0 && targetChatID != 0 && isSameChat(targetChatID, logChatID) && c.InlineManager() != nil {
		im := c.InlineManager()
		if im.IsComplete() {
			botClient := im.GetBotAPI()
			if botClient != nil {
				var topicID int
				dummyReq := &tg.MessagesSendMessageRequest{}
				for _, opt := range opts {
					opt(dummyReq)
				}
				if dummyReq.ReplyTo != nil {
					if replyObj, ok := dummyReq.ReplyTo.(*tg.InputReplyToMessage); ok {
						topicID = replyObj.ReplyToMsgID
					}
				}

				targetBotChatID := c.ToBotAPIChatID(targetChatID)
				var fileBytes []byte
				var filename string = "file.bin"
				if named, ok := file.(interface{ Name() string }); ok {
					filename = named.Name()
				}
				var isURL bool

				switch f := file.(type) {
				case string:
					if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
						isURL = true
					} else {
						data, err := os.ReadFile(f) //nolint:gosec
						if err == nil {
							fileBytes = data
							filename = filepath.Base(f)
						}
					}
				case []byte:
					fileBytes = f
				case io.Reader:
					data, err := io.ReadAll(f)
					if err == nil {
						fileBytes = data
					}
				}

				if isURL {
					fileURL := file.(string)
					ext := strings.ToLower(filepath.Ext(fileURL))
					if idx := strings.Index(ext, "?"); idx != -1 {
						ext = ext[:idx]
					}
					if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
						return sendBotFileContext(ctx, botClient, targetBotChatID, tgbotapi.FileURL(fileURL), caption, topicID, true)
					} else {
						return sendBotFileContext(ctx, botClient, targetBotChatID, tgbotapi.FileURL(fileURL), caption, topicID, false)
					}
				} else if len(fileBytes) > 0 {
					fb := tgbotapi.FileBytes{Name: filename, Bytes: fileBytes}
					ext := strings.ToLower(filepath.Ext(filename))
					if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
						return sendBotFileContext(ctx, botClient, targetBotChatID, fb, caption, topicID, true)
					} else {
						return sendBotFileContext(ctx, botClient, targetBotChatID, fb, caption, topicID, false)
					}
				}
			}
		}
	}

	peer, err := c.resolveRequestPeerContext(ctx, chat)
	if err != nil {
		return nil, err
	}

	up := uploader.NewUploader(c.rawAPI)
	var inputFile tg.InputFileClass
	var filename string
	var mimeType string = "application/octet-stream"

	switch v := file.(type) {
	case string:
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, v, nil)
			if err != nil {
				return nil, err
			}
			resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("unexpected HTTP status: %d", resp.StatusCode)
			}
			data, err := utils.ReadResponseBodyLimited(resp, 50*1024*1024)
			if err != nil {
				return nil, err
			}
			filename = filepath.Base(v)
			if idx := strings.Index(filename, "?"); idx != -1 {
				filename = filename[:idx]
			}
			if filename == "" {
				filename = "file.bin"
			}
			inputFile, err = up.FromBytes(ctx, filename, data)
			if err != nil {
				return nil, err
			}
		} else {
			filename = filepath.Base(v)
			inputFile, err = up.FromPath(ctx, v)
			if err != nil {
				return nil, err
			}
		}
	case []byte:
		filename = "file.bin"
		if named, ok := file.(interface{ Name() string }); ok {
			filename = named.Name()
		}
		inputFile, err = up.FromBytes(ctx, filename, v)
		if err != nil {
			return nil, err
		}
	case io.Reader:
		filename = "file.bin"
		if named, ok := v.(interface{ Name() string }); ok {
			filename = named.Name()
		}
		inputFile, err = up.FromReader(ctx, filename, v)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported file type: %T", file)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".mp4":
		mimeType = "video/mp4"
	}

	var media tg.InputMediaClass
	if mimeType == "image/jpeg" || mimeType == "image/png" {
		photoMedia := &tg.InputMediaUploadedPhoto{
			File: inputFile,
		}
		photoMedia.SetFlags()
		media = photoMedia
	} else {
		media = &tg.InputMediaUploadedDocument{
			File:     inputFile,
			MimeType: mimeType,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: filename},
			},
		}
	}

	plainCaption, captionEntities := parseHTML(caption)
	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		Message:  plainCaption,
		Entities: captionEntities,
		RandomID: rand.Int63(), //nolint:gosec
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesSendMedia(ctx, req)
	return res, err
}

func sendBotFileContext(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, file tgbotapi.RequestFileData, caption string, topicID int, photo bool) (tgbotapi.Message, error) {
	var config tgbotapi.Chattable
	if photo {
		request := tgbotapi.NewPhoto(chatID, file)
		request.Caption, request.ParseMode, request.MessageThreadID = caption, tgbotapi.ModeHTML, topicID
		config = request
	} else {
		request := tgbotapi.NewDocument(chatID, file)
		request.Caption, request.ParseMode, request.MessageThreadID = caption, tgbotapi.ModeHTML, topicID
		config = request
	}
	resp, err := bot.RequestWithContext(ctx, config)
	if err != nil {
		return tgbotapi.Message{}, err
	}
	var message tgbotapi.Message
	err = json.Unmarshal(resp.Result, &message)
	return message, err
}

func (c *CustomTelegramClient) SendMessage(chat ChatRef, message string) (any, error) {
	return c.SendMessageWithOptions(chat, message)
}

func (c *CustomTelegramClient) SendMessageContext(ctx context.Context, chat ChatRef, message string) (any, error) {
	return c.SendMessageWithOptionsContext(ctx, chat, message)
}

func (c *CustomTelegramClient) SendMessageWithOptions(chat ChatRef, message string, opts ...MsgOption) (any, error) {
	return c.SendMessageWithOptionsContext(nil, chat, message, opts...)
}

func (c *CustomTelegramClient) SendMessageWithOptionsContext(ctx context.Context, chat ChatRef, message string, opts ...MsgOption) (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targetChatID := chat.ID()

	logChatID, err := c.GetLogChatIDChecked()
	if err != nil {
		return nil, fmt.Errorf("get log chat ID: %w", err)
	}
	if logChatID != 0 && targetChatID != 0 && isSameChat(targetChatID, logChatID) && c.InlineManager() != nil {
		im := c.InlineManager()
		if im.IsComplete() {
			botClient := im.GetBotAPI()
			if botClient != nil {
				var topicID int
				dummyReq := &tg.MessagesSendMessageRequest{}
				for _, opt := range opts {
					opt(dummyReq)
				}
				if dummyReq.ReplyTo != nil {
					if replyObj, ok := dummyReq.ReplyTo.(*tg.InputReplyToMessage); ok {
						topicID = replyObj.ReplyToMsgID
					}
				}

				targetBotChatID := c.ToBotAPIChatID(targetChatID)
				request := tgbotapi.NewMessage(targetBotChatID, message)
				request.ParseMode, request.MessageThreadID = tgbotapi.ModeHTML, topicID
				resp, err := botClient.RequestWithContext(ctx, request)
				if err != nil {
					return nil, err
				}
				var sent tgbotapi.Message
				err = json.Unmarshal(resp.Result, &sent)
				return sent, err
			}
		}
	}

	peer, err := c.resolveRequestPeerContext(ctx, chat)
	if err != nil {
		return nil, err
	}

	plainText, entities := parseHTML(message)
	req := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  plainText,
		Entities: entities,
		RandomID: rand.Int63(), //nolint:gosec
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesSendMessage(ctx, req)
	return res, err
}

func (c *CustomTelegramClient) EditMessage(chat ChatRef, msgID int64, text string, opts ...MsgOption) (any, error) {
	return c.EditMessageContext(nil, chat, msgID, text, opts...)
}

func (c *CustomTelegramClient) EditMessageContext(ctx context.Context, chat ChatRef, msgID int64, text string, opts ...MsgOption) (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peer, err := c.resolveRequestPeerContext(ctx, chat)
	if err != nil {
		return nil, err
	}

	plainText, entities := parseHTML(text)
	req := &tg.MessagesEditMessageRequest{
		Peer:     peer,
		ID:       int(msgID),
		Message:  plainText,
		Entities: entities,
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := c.rawAPI.MessagesEditMessage(ctx, req)
	return res, err
}

func (c *CustomTelegramClient) DeleteMessage(chat ChatRef, msgID int64) error {
	return c.DeleteMessageContext(nil, chat, msgID)
}

func (c *CustomTelegramClient) DeleteMessageContext(ctx context.Context, chat ChatRef, msgID int64) error {
	if c.rawAPI == nil {
		return ErrClientNotInitialized
	}
	ctx = c.rpcContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	peer, err := c.resolveRequestPeerContext(ctx, chat)
	if err != nil {
		return err
	}

	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		_, err = c.rawAPI.ChannelsDeleteMessages(ctx,
			&tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{
					ChannelID:  ch.ChannelID,
					AccessHash: ch.AccessHash,
				},
				ID: []int{int(msgID)},
			})
		return err
	}

	_, err = c.rawAPI.MessagesDeleteMessages(ctx,
		&tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{int(msgID)},
		})
	return err
}

func (c *CustomTelegramClient) rpcContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}
