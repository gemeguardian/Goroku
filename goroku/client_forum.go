package goroku

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"goroku/goroku/cache"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

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
	var peer tg.InputPeerClass
	if ch, ok := channelPeer.(*tg.InputPeerChannel); ok {
		peer = ch
	} else {
		return 0, fmt.Errorf("peer is not a channel")
	}

	req := &tg.MessagesCreateForumTopicRequest{
		Peer:     peer,
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

	res, err := c.rawAPI.MessagesCreateForumTopic(c.ctx, req)
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
	if _, ok := channelPeer.(*tg.InputPeerChannel); !ok {
		return 0, fmt.Errorf("peer is not a channel")
	}

	res, err := c.rawAPI.MessagesGetForumTopics(c.ctx, &tg.MessagesGetForumTopicsRequest{
		Peer:  channelPeer,
		Limit: 100,
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
