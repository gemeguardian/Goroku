package goroku

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// Extracted cache and resolve components are now in separate files cache_*.go
// M6.5: transport/api limiter/messages/answer/forum split into client_*.go / message_answer.go

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
