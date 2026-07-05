package goroku

import (
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
	return c.sendFileInternal(chat, file, caption, opts...)
}

func (c *CustomTelegramClient) resolveRequestPeer(chat ChatRef) (tg.InputPeerClass, error) {
	peer, err := c.ResolvePeerRef(chat)
	if err != nil {
		return nil, fmt.Errorf("resolve peer: %w", err)
	}
	return peer, nil
}

func (c *CustomTelegramClient) sendFileInternal(chat ChatRef, file any, caption string, opts ...MsgOption) (any, error) {
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

	logChatID := c.GetLogChatID()
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
						return SendPhotoWithTopic(botClient, targetBotChatID, tgbotapi.FileURL(fileURL), caption, topicID)
					} else {
						return SendDocumentWithTopic(botClient, targetBotChatID, tgbotapi.FileURL(fileURL), caption, topicID)
					}
				} else if len(fileBytes) > 0 {
					fb := tgbotapi.FileBytes{Name: filename, Bytes: fileBytes}
					ext := strings.ToLower(filepath.Ext(filename))
					if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
						return SendPhotoWithTopic(botClient, targetBotChatID, fb, caption, topicID)
					} else {
						return SendDocumentWithTopic(botClient, targetBotChatID, fb, caption, topicID)
					}
				}
			}
		}
	}

	peer, err := c.resolveRequestPeer(chat)
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
			data, err := utils.DownloadURLLimited(&http.Client{Timeout: 30 * time.Second}, v, 50*1024*1024)
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
			inputFile, err = up.FromBytes(c.ctx, filename, data)
			if err != nil {
				return nil, err
			}
		} else {
			filename = filepath.Base(v)
			inputFile, err = up.FromPath(c.ctx, v)
			if err != nil {
				return nil, err
			}
		}
	case []byte:
		filename = "file.bin"
		if named, ok := file.(interface{ Name() string }); ok {
			filename = named.Name()
		}
		inputFile, err = up.FromBytes(c.ctx, filename, v)
		if err != nil {
			return nil, err
		}
	case io.Reader:
		filename = "file.bin"
		if named, ok := v.(interface{ Name() string }); ok {
			filename = named.Name()
		}
		inputFile, err = up.FromReader(c.ctx, filename, v)
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
	res, err := c.rawAPI.MessagesSendMedia(c.ctx, req)
	return res, err
}

func (c *CustomTelegramClient) SendMessage(chat ChatRef, message string) (any, error) {
	return c.SendMessageWithOptions(chat, message)
}

func (c *CustomTelegramClient) SendMessageWithOptions(chat ChatRef, message string, opts ...MsgOption) (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	targetChatID := chat.ID()

	logChatID := c.GetLogChatID()
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
				return SendMessageWithTopic(botClient, targetBotChatID, message, topicID)
			}
		}
	}

	peer, err := c.resolveRequestPeer(chat)
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
	res, err := c.rawAPI.MessagesSendMessage(c.ctx, req)
	return res, err
}

func (c *CustomTelegramClient) EditMessage(chat ChatRef, msgID int64, text string, opts ...MsgOption) (any, error) {
	if c.rawAPI == nil {
		return nil, ErrClientNotInitialized
	}
	peer, err := c.resolveRequestPeer(chat)
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
	res, err := c.rawAPI.MessagesEditMessage(c.ctx, req)
	return res, err
}

func (c *CustomTelegramClient) DeleteMessage(chat ChatRef, msgID int64) error {
	if c.rawAPI == nil {
		return ErrClientNotInitialized
	}
	peer, err := c.resolveRequestPeer(chat)
	if err != nil {
		return err
	}

	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		_, err = c.rawAPI.ChannelsDeleteMessages(c.ctx,
			&tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{
					ChannelID:  ch.ChannelID,
					AccessHash: ch.AccessHash,
				},
				ID: []int{int(msgID)},
			})
		return err
	}

	_, err = c.rawAPI.MessagesDeleteMessages(c.ctx,
		&tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{int(msgID)},
		})
	return err
}
